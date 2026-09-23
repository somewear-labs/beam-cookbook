import { ipcMain, shell } from 'electron';
import { readdirSync } from 'fs';
import { deserializeContent } from './proto-deserializer';
import { parseSensorPayload } from './sensor-parser';

const BEAM_BASE = 'http://localhost:9091';

// Enrich a payload in-place with sensorType/sensorName/sensorData if it
// carries a SWL-framed sensor payload in contentBytes.
async function enrichWithSensorData(p: Record<string, unknown>): Promise<void> {
  if (p.sensorType !== undefined) return; // already done
  if (typeof p.contentBytes !== 'string') return;
  const sensor = await parseSensorPayload(p.contentBytes);
  if (!sensor) return;
  p.sensorType = sensor.sensorType;
  p.sensorName = sensor.sensorName;
  p.sensorData = sensor.sensorData;
}

// ─── Fetch helper ─────────────────────────────────────────────────────────────

export async function beamFetch<T>(
  path: string,
  options: RequestInit = {},
  timeoutMs = 8000
): Promise<T> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(`${BEAM_BASE}${path}`, {
      ...options,
      signal: controller.signal,
      headers: { 'Content-Type': 'application/json', ...(options.headers ?? {}) }
    });
    if (!response.ok) throw new Error(`Beam API error ${response.status} on ${path}`);
    const text = await response.text();
    return (text.trim() ? JSON.parse(text) : null) as T;
  } finally {
    clearTimeout(timer);
  }
}

// ─── SSE tail helper ──────────────────────────────────────────────────────────
// Runs a reconnecting SSE loop. Calls onEvent for each parsed JSON event.
// Returns the AbortController so the caller can stop it.

function sseLoop(
  path: string,
  controller: AbortController,
  onEvent: (data: unknown) => void
): void {
  const delay = (ms: number) =>
    new Promise<void>((resolve) => {
      const onAbort = () => { clearTimeout(t); resolve(); };
      const t = setTimeout(() => {
        controller.signal.removeEventListener('abort', onAbort);
        resolve();
      }, ms);
      controller.signal.addEventListener('abort', onAbort, { once: true });
    });

  (async () => {
    while (!controller.signal.aborted) {
      try {
        const response = await fetch(`${BEAM_BASE}${path}`, {
          signal: controller.signal,
          headers: { Accept: 'text/event-stream' }
        });

        if (!response.ok || !response.body) throw new Error(`SSE ${path} failed: ${response.status}`);

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (!controller.signal.aborted) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() ?? '';
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const raw = line.slice(6).trim();
              if (raw) {
                try { onEvent(JSON.parse(raw)); } catch { /* skip non-JSON */ }
              }
            }
          }
        }
      } catch {
        if (controller.signal.aborted) break;
        await delay(3000);
      }
      if (!controller.signal.aborted) await delay(1000);
    }
  })();
}

// ─── Active tail registries ───────────────────────────────────────────────────

const _payloadTails = new Map<number, AbortController>();
const _networkTails = new Map<number, AbortController>();
const _satTails = new Map<number, AbortController>();
const _queueReportTails = new Map<number, AbortController>();
let _usbInterval: ReturnType<typeof setInterval> | null = null;

// ─── Setup / teardown ─────────────────────────────────────────────────────────

export function setupBeamHandlers(): void {
  // ── One-shot requests ──────────────────────────────────────────────────────

  ipcMain.handle('beam:health', async () => {
    try {
      const data = await beamFetch<{ status: string }>('/api/health');
      return data.status === 'UP';
    } catch { return false; }
  });

  ipcMain.handle('beam:auth-state', async () => {
    return beamFetch('/api/auth/state');
  });

  ipcMain.handle('beam:set-api-key', async (_e, apiKey: string) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 8000);
    try {
      const response = await fetch(`${BEAM_BASE}/api/auth/api-key`, {
        method: 'POST',
        body: JSON.stringify({ apiKey }),
        headers: { 'Content-Type': 'application/json' },
        signal: controller.signal
      });
      const text = await response.text();
      const body = text.trim() ? JSON.parse(text) : {};
      if (!response.ok) {
        return { success: false, isAuthenticated: false, message: body.message || `Authentication failed (HTTP ${response.status})` };
      }
      return body;
    } finally {
      clearTimeout(timer);
    }
  });

  ipcMain.handle('beam:system', async () => {
    try { return await beamFetch('/api/system'); } catch { return null; }
  });

  ipcMain.handle('beam:workspaces', () => beamFetch('/api/workspaces'));

  ipcMain.handle('beam:activate-workspace', (_e, workspaceId: string) =>
    beamFetch('/api/workspace/activate', { method: 'POST', body: JSON.stringify({ workspaceId }) })
  );

  ipcMain.handle('beam:device-connect', async (_e, port?: string) => {
    try {
      await beamFetch('/api/device/connect', { method: 'POST', body: port ? JSON.stringify({ port }) : '{}' });
      return true;
    } catch {
      return false;
    }
  });

  ipcMain.handle('beam:device-disconnect', async () => {
    try {
      await beamFetch('/api/device/disconnect', { method: 'POST', body: '{}' });
      return true;
    } catch {
      return false;
    }
  });

  ipcMain.handle('beam:provision-edge', (_e, name: string) =>
    beamFetch('/api/edge-compute/provision', { method: 'POST', body: JSON.stringify({ name }) })
  );

  ipcMain.handle('beam:validate-server', (_e, host: string) =>
    beamFetch('/api/auth/validate-server', { method: 'POST', body: JSON.stringify({ host }) })
  );

  ipcMain.handle('beam:open-external', (_e, url: string) => {
    shell.openExternal(url);
  });

  ipcMain.handle('beam:check-auth-token', (_e, nonce: string) =>
    beamFetch(`/api/auth/token?nonce=${encodeURIComponent(nonce)}`, {}, 5000)
  );

  ipcMain.handle('beam:fetch-organizations', (_e, nonce: string) =>
    beamFetch(`/api/auth/organizations?nonce=${encodeURIComponent(nonce)}`)
  );

  ipcMain.handle('beam:create-api-key', (_e, organizationId: string, nonce: string) =>
    beamFetch('/api/auth/create-api-key', { method: 'POST', body: JSON.stringify({ organizationId, nonce }) })
  );

  ipcMain.handle('beam:identities', () => beamFetch('/api/identities'));

  ipcMain.handle('beam:payloads', async () => {
    const payloads = await beamFetch<Record<string, unknown>[]>('/api/payloads');
    const seen = new Set<string>();
    return Promise.all(
      payloads
        .filter((p) => String(p.channel ?? '').toLowerCase() !== 'none')
        .filter((p) => {
          if (!p.datagramId || !p.channel) return true;
          const key = `${p.datagramId}_${p.channel}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .map(async (p) => {
          // Sensor check first — SWL-framed payloads must not be fed to the RPC decoder.
          await enrichWithSensorData(p);
          if (p.sensorType === undefined && !p.content && typeof p.type === 'string' && typeof p.contentBytes === 'string') {
            p.content = await deserializeContent(p.type, p.contentBytes);
          }
          return p;
        })
    );
  });

  ipcMain.handle('beam:network', () => beamFetch('/api/device/network'));

  ipcMain.handle('beam:queue', (_e, channel?: string) => {
    const path = channel ? `/api/queue?channel=${encodeURIComponent(channel)}` : '/api/queue';
    return beamFetch(path);
  });

  ipcMain.handle('beam:cancel-package', (_e, parcelId: number, channel?: string) => {
    const path = channel
      ? `/api/package/${parcelId}/cancel?channel=${encodeURIComponent(channel)}`
      : `/api/package/${parcelId}/cancel`;
    return beamFetch(path, { method: 'POST', body: '' });
  });

  ipcMain.handle('beam:flush-queue', async () => {
    const result = await beamFetch<{ flushed: boolean }>('/api/device/flush-queue', { method: 'POST', body: '' });
    return { flushed: result?.flushed ? 1 : 0, total: 1 };
  });

  ipcMain.handle('beam:queue-report', () =>
    beamFetch('/api/device/queue-report/tail?format=json')
  );


  // ── SSE tails ──────────────────────────────────────────────────────────────

  ipcMain.on('beam:tail-payloads', async (event) => {
    const id = event.sender.id;
    _payloadTails.get(id)?.abort();
    const ctrl = new AbortController();
    _payloadTails.set(id, ctrl);

    sseLoop('/api/payloads/tail', ctrl, async (raw) => {
      const p = raw as Record<string, unknown>;
      if (String(p.channel ?? '').toLowerCase() === 'none') return;
      // The tail stream omits contentBytes — fetch it for IPv4Datagram payloads.
      if (typeof p.contentBytes !== 'string' && typeof p.datagramId === 'string' && p.type === 'IPv4Datagram') {
        try {
          const full = await beamFetch<Record<string, unknown>>(`/api/payloads/${p.datagramId}`);
          if (typeof full?.contentBytes === 'string') p.contentBytes = full.contentBytes;
        } catch { /* proceed without contentBytes */ }
      }
      // Sensor check first — SWL-framed payloads must not be fed to the RPC decoder.
      await enrichWithSensorData(p);
      if (p.sensorType === undefined && !p.content && typeof p.type === 'string' && typeof p.contentBytes === 'string') {
        p.content = await deserializeContent(p.type, p.contentBytes);
      }
      if (!event.sender.isDestroyed()) event.sender.send('beam:payload-event', p);
    });
  });

  ipcMain.on('beam:stop-tail', (event) => {
    _payloadTails.get(event.sender.id)?.abort();
    _payloadTails.delete(event.sender.id);
  });

  ipcMain.on('beam:tail-network', (event) => {
    const id = event.sender.id;
    _networkTails.get(id)?.abort();
    const ctrl = new AbortController();
    _networkTails.set(id, ctrl);

    sseLoop('/api/device/network/tail', ctrl, (state) => {
      if (!event.sender.isDestroyed()) event.sender.send('beam:network-event', state);
    });
  });

  ipcMain.on('beam:stop-network-tail', (event) => {
    _networkTails.get(event.sender.id)?.abort();
    _networkTails.delete(event.sender.id);
  });

  ipcMain.on('beam:tail-satellite-quality', (event) => {
    const id = event.sender.id;
    _satTails.get(id)?.abort();
    const ctrl = new AbortController();
    _satTails.set(id, ctrl);

    sseLoop('/api/device/satellite/quality/tail', ctrl, (q) => {
      if (!event.sender.isDestroyed()) event.sender.send('beam:satellite-quality-event', q);
    });
  });

  ipcMain.on('beam:stop-satellite-quality-tail', (event) => {
    _satTails.get(event.sender.id)?.abort();
    _satTails.delete(event.sender.id);
  });

  ipcMain.on('beam:tail-queue-report', (event) => {
    const id = event.sender.id;
    _queueReportTails.get(id)?.abort();
    const ctrl = new AbortController();
    _queueReportTails.set(id, ctrl);

    sseLoop('/api/device/queue-report/tail', ctrl, (report) => {
      if (!event.sender.isDestroyed()) event.sender.send('beam:queue-report-event', report);
    });
  });

  ipcMain.on('beam:stop-queue-report-tail', (event) => {
    _queueReportTails.get(event.sender.id)?.abort();
    _queueReportTails.delete(event.sender.id);
  });

  // ── USB auto-connect ───────────────────────────────────────────────────────

  let knownDevices = _usbDevices();

  _usbInterval = setInterval(async () => {
    const current = _usbDevices();
    const added = [...current].filter((d) => !knownDevices.has(d));
    knownDevices = current;

    if (added.length > 0) {
      console.log('[Beam] USB device(s) plugged:', added.join(', '));
      await new Promise((r) => setTimeout(r, 1500));
      try {
        await beamFetch('/api/device/connect', { method: 'POST', body: '{}' });
        console.log('[Beam] Auto-connected via USB plug event');
      } catch { /* 412 = already connected */ }
    }
  }, 2000);
}

export function stopBeamHandlers(): void {
  if (_usbInterval !== null) {
    clearInterval(_usbInterval);
    _usbInterval = null;
  }
  for (const ctrl of _payloadTails.values()) ctrl.abort();
  for (const ctrl of _networkTails.values()) ctrl.abort();
  for (const ctrl of _satTails.values()) ctrl.abort();
  for (const ctrl of _queueReportTails.values()) ctrl.abort();
  _payloadTails.clear();
  _networkTails.clear();
  _satTails.clear();
  _queueReportTails.clear();
}

export function cleanupWindowTails(webContentsId: number): void {
  _payloadTails.get(webContentsId)?.abort();
  _payloadTails.delete(webContentsId);
  _networkTails.get(webContentsId)?.abort();
  _networkTails.delete(webContentsId);
  _satTails.get(webContentsId)?.abort();
  _satTails.delete(webContentsId);
  _queueReportTails.get(webContentsId)?.abort();
  _queueReportTails.delete(webContentsId);
}

function _usbDevices(): Set<string> {
  try {
    return new Set(readdirSync('/dev').filter((f) => f.startsWith('tty.usbmodem')));
  } catch {
    return new Set();
  }
}
