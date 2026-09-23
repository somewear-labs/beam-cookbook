import { useEffect, useRef, useState, useCallback } from 'react';
import { beamApi } from '../services/beamApi';
import type { PayloadEvent } from '../services/beamApi';

const SENSOR_TYPE_SEISMOGRAPH = 5;
const SENSOR_TYPE_UGS = 1;
const BUFFER_SIZE = 200;

const CW = 340;
const CH_H = 36;
const N_CH = 3;
const CANVAS_H = CH_H * N_CH;

const CHANNELS: Array<{ key: 'bhz' | 'bhn' | 'bhe'; color: string; glow: string }> = [
  { key: 'bhz', color: '#00b4d8', glow: 'rgba(0,180,216,0.35)' },
  { key: 'bhn', color: '#2d9e4f', glow: 'rgba(45,158,79,0.30)' },
  { key: 'bhe', color: '#ffaa00', glow: 'rgba(255,170,0,0.25)' },
];

interface Sample {
  bhz: number;
  bhn: number;
  bhe: number;
  event: boolean;
}

interface DisplayInfo {
  stationId: string;
  network: string;
  pgv: number;
  intensity: number;
  eventId?: string;
  magnitude?: number;
  threatType?: string;
}

const MMI = ['', 'I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X', 'XI', 'XII'];

function pgvToStr(pgv: number): string {
  if (pgv === 0) return '—';
  return pgv.toExponential(2);
}

export default function SeismographPanel() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const bufRef = useRef<Sample[]>([]);
  const seenRef = useRef<Set<string>>(new Set());
  const everHadDataRef = useRef(false);
  const [panelVisible, setPanelVisible] = useState(false);
  const [info, setInfo] = useState<DisplayInfo>({
    stationId: 'RCOE',
    network: 'CI',
    pgv: 0,
    intensity: 1,
  });
  const [eventBanner, setEventBanner] = useState<string | null>(null);
  const bannerTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    canvas.width = CW * dpr;
    canvas.height = CANVAS_H * dpr;
    ctx.scale(dpr, dpr);

    const buf = bufRef.current;
    ctx.fillStyle = '#090e15';
    ctx.fillRect(0, 0, CW, CANVAS_H);

    ctx.strokeStyle = '#1e3d5a';
    ctx.lineWidth = 1;
    for (let i = 0; i < N_CH; i++) {
      const midY = i * CH_H + CH_H / 2;
      ctx.beginPath(); ctx.moveTo(0, midY); ctx.lineTo(CW, midY); ctx.stroke();
      if (i > 0) {
        ctx.beginPath(); ctx.moveTo(0, i * CH_H); ctx.lineTo(CW, i * CH_H); ctx.stroke();
      }
    }

    if (buf.length < 2) return;

    const xStep = CW / (BUFFER_SIZE - 1);

    CHANNELS.forEach(({ key, color, glow }, ci) => {
      const topY = ci * CH_H;
      const midY = topY + CH_H / 2;
      const halfH = CH_H / 2 - 2;

      let maxAmp = 1e-12;
      for (const s of buf) {
        const v = Math.abs(s[key]);
        if (v > maxAmp) maxAmp = v;
      }
      const scale = halfH / maxAmp;

      let inEvt = false;
      let evtX = 0;
      buf.forEach((s, i) => {
        const x = (BUFFER_SIZE - buf.length + i) * xStep;
        if (s.event && !inEvt) { inEvt = true; evtX = x; }
        if (!s.event && inEvt) {
          ctx.fillStyle = 'rgba(255,170,0,0.07)';
          ctx.fillRect(evtX, topY, x - evtX, CH_H);
          inEvt = false;
        }
      });
      if (inEvt) {
        ctx.fillStyle = 'rgba(255,170,0,0.07)';
        ctx.fillRect(evtX, topY, (BUFFER_SIZE - 1) * xStep - evtX, CH_H);
      }

      ctx.save();
      ctx.shadowBlur = 3;
      ctx.shadowColor = glow;
      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      buf.forEach((s, i) => {
        const x = (BUFFER_SIZE - buf.length + i) * xStep;
        const y = midY - s[key] * scale;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.stroke();
      ctx.restore();

      ctx.fillStyle = color;
      ctx.font = 'bold 8px "Roboto Condensed", monospace';
      ctx.fillText(key.toUpperCase(), 3, topY + 9);
    });
  }, []);

  const ingestPayload = useCallback((payload: PayloadEvent) => {
    const p = payload as Record<string, unknown>;
    const isSeismo = p.sensorType === SENSOR_TYPE_SEISMOGRAPH;
    const isUGS = p.sensorType === SENSOR_TYPE_UGS;
    if (!isSeismo && !isUGS) return false;

    const data = p.sensorData as Record<string, unknown> | undefined;
    if (!data) return false;

    let bhz: number[], bhn: number[], bhe: number[], isEvent: boolean;

    if (isUGS) {
      // UGS sends seismic_e/n/z arrays — map to BHE/BHN/BHZ channels.
      const raw_e = (data.SeismicE as number[] | undefined) ?? (data.seismic_e as number[] | undefined) ?? [];
      const raw_n = (data.SeismicN as number[] | undefined) ?? (data.seismic_n as number[] | undefined) ?? [];
      const raw_z = (data.SeismicZ as number[] | undefined) ?? (data.seismic_z as number[] | undefined) ?? [];
      bhz = raw_z;
      bhn = raw_n;
      bhe = raw_e;
      const threat = (data.ThreatType as string | undefined) ?? (data.threat_type as string | undefined) ?? 'NONE';
      isEvent = threat !== 'NONE';
    } else {
      bhz = (data.channel_bhz as number[] | undefined) ?? [];
      bhn = (data.channel_bhn as number[] | undefined) ?? [];
      bhe = (data.channel_bhe as number[] | undefined) ?? [];
      isEvent = !!data.event_id;
    }

    const samples: Sample[] = bhz.map((_, i) => ({
      bhz: bhz[i] ?? 0,
      bhn: bhn[i] ?? 0,
      bhe: bhe[i] ?? 0,
      event: isEvent,
    }));

    bufRef.current = [...bufRef.current, ...samples].slice(-BUFFER_SIZE);

    if (!everHadDataRef.current) {
      everHadDataRef.current = true;
      setPanelVisible(true);
    }

    if (isUGS) {
      const nodeId = (data.NodeID as string | undefined) ?? (data.node_id as string | undefined) ?? 'UGS';
      const peak = (data.PeakAmplitude as number | undefined) ?? (data.peak_amplitude as number | undefined) ?? 0;
      const eventId = (data.EventID as string | undefined) ?? (data.event_id as string | undefined);
      const threat = (data.ThreatType as string | undefined) ?? (data.threat_type as string | undefined) ?? 'NONE';
      setInfo({
        stationId: nodeId,
        network: 'UGS',
        pgv: peak,
        intensity: 1,
        eventId,
        threatType: threat,
      });
      if (isEvent && eventId) {
        const threat = (data.ThreatType as string | undefined) ?? (data.threat_type as string | undefined) ?? '';
        const conf = (data.ConfidencePct as number | undefined) ?? (data.confidence_pct as number | undefined);
        setEventBanner(`${eventId} ${threat}${conf !== undefined ? ` ${conf}%` : ''}`);
        if (bannerTimer.current) clearTimeout(bannerTimer.current);
        bannerTimer.current = setTimeout(() => setEventBanner(null), 30_000);
      }
    } else {
      setInfo({
        stationId: (data.station_id as string | undefined) ?? 'RCOE',
        network: (data.network_code as string | undefined) ?? 'CI',
        pgv: (data.pgv_ms as number | undefined) ?? 0,
        intensity: (data.intensity as number | undefined) ?? 1,
        eventId: data.event_id as string | undefined,
        magnitude: data.magnitude as number | undefined,
      });

      if (isEvent) {
        const mag = data.magnitude != null ? ` M${(data.magnitude as number).toFixed(1)}` : '';
        setEventBanner(`${data.event_id}${mag}`);
        if (bannerTimer.current) clearTimeout(bannerTimer.current);
        bannerTimer.current = setTimeout(() => setEventBanner(null), 30_000);
      }
    }
    return true;
  }, []);

  // Draw once after canvas is first mounted (panelVisible flips true).
  useEffect(() => {
    if (panelVisible) draw();
  }, [panelVisible, draw]);

  // Poll REST every 5 s. SSE tail events lack contentBytes so we can't detect sensor type
  // from them — polling is the only way to get live seismograph updates.
  useEffect(() => {
    const poll = async () => {
      try {
        const payloads = await beamApi.payloads();
        const newPayloads = payloads
          .filter((p) => {
            const raw = p as Record<string, unknown>;
            if (raw.sensorType !== SENSOR_TYPE_SEISMOGRAPH && raw.sensorType !== SENSOR_TYPE_UGS) return false;
            const key = String(p.datagramId ?? p.id ?? `${p.timestamp}_${p.channel}`);
            if (seenRef.current.has(key)) return false;
            seenRef.current.add(key);
            return true;
          })
          .sort((a, b) => {
            const ta = a.timestamp ? new Date(a.timestamp as string).getTime() : 0;
            const tb = b.timestamp ? new Date(b.timestamp as string).getTime() : 0;
            return ta - tb;
          });

        let added = false;
        for (const p of newPayloads) {
          if (ingestPayload(p)) added = true;
        }
        if (added && canvasRef.current) draw();
      } catch { /* daemon not reachable */ }
    };

    poll();
    const interval = setInterval(poll, 5000);
    return () => clearInterval(interval);
  }, [ingestPayload, draw]);

  const handleClear = useCallback(() => {
    bufRef.current = [];
    draw();
  }, [draw]);

  if (!panelVisible) return null;

  const mmi = MMI[info.intensity] ?? String(info.intensity);

  return (
    <div className="seismograph-panel">
      <div className="seismograph-header">
        <span className="seismograph-title">{info.network === 'UGS' ? 'UGS' : 'SEISMO'}</span>
        <span className="seismograph-station">{info.network === 'UGS' ? info.stationId : `${info.network}.${info.stationId}`}</span>
        <span className="seismograph-stat">
          PEAK <span className="seismograph-stat-val">{pgvToStr(info.pgv)}</span>
        </span>
        {info.network !== 'UGS' && (
          <span className="seismograph-stat">
            MMI <span className="seismograph-stat-val">{mmi}</span>
          </span>
        )}
        {info.network === 'UGS' && info.threatType && (
          <span className="seismograph-stat">
            THREAT <span className={`seismograph-stat-val${info.threatType !== 'NONE' ? ' seismograph-threat-active' : ''}`}>{info.threatType}</span>
          </span>
        )}
        {eventBanner && (
          <span className="seismograph-event-badge">⚡ {eventBanner}</span>
        )}
        <span className="seismograph-live">● LIVE</span>
        <button className="seismograph-clear-btn" onClick={handleClear}>CLR</button>
      </div>
      <canvas
        ref={canvasRef}
        width={CW}
        height={CANVAS_H}
        className="seismograph-canvas"
      />
    </div>
  );
}
