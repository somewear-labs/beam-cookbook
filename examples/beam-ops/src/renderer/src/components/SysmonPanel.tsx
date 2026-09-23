import { useCallback, useEffect, useRef, useState } from 'react';
import { beamApi } from '../services/beamApi';
import type { PayloadEvent } from '../services/beamApi';

const SENSOR_TYPE_SYSMON = 7;
const BUFFER_SIZE = 100;

const CW = 340;
const CH_H = 30;
const N_CH = 2;
const CANVAS_H = CH_H * N_CH;

interface Sample {
  cpu: number;
  mem: number;
}

interface DisplayInfo {
  nodeId: string;
  cpuPct: number;
  memUsedMb: number;
  memTotalMb: number;
  memPct: number;
  diskUsedGb: number;
  diskTotalGb: number;
  diskPct: number;
  loadAvg1m: number;
  uptimeSeconds: number;
}

function fmtUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h${m.toString().padStart(2, '0')}m`;
}

export default function SysmonPanel() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const bufRef = useRef<Sample[]>([]);
  const seenRef = useRef<Set<string>>(new Set());
  const everHadDataRef = useRef(false);
  const [panelVisible, setPanelVisible] = useState(false);
  const [info, setInfo] = useState<DisplayInfo>({
    nodeId: '—',
    cpuPct: 0,
    memUsedMb: 0,
    memTotalMb: 0,
    memPct: 0,
    diskUsedGb: 0,
    diskTotalGb: 0,
    diskPct: 0,
    loadAvg1m: 0,
    uptimeSeconds: 0,
  });

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    canvas.width = CW * dpr;
    canvas.height = CANVAS_H * dpr;
    ctx.scale(dpr, dpr);

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

    const buf = bufRef.current;
    if (buf.length < 2) return;

    const xStep = CW / (BUFFER_SIZE - 1);

    const channels: Array<{ key: keyof Sample; label: string; color: string; glow: string }> = [
      { key: 'cpu', label: 'CPU', color: '#00b4d8', glow: 'rgba(0,180,216,0.35)' },
      { key: 'mem', label: 'MEM', color: '#2d9e4f', glow: 'rgba(45,158,79,0.30)' },
    ];

    channels.forEach(({ key, label, color, glow }, ci) => {
      const topY = ci * CH_H;
      const botY = topY + CH_H;
      const usableH = CH_H - 4;

      // Draw filled area under the line first
      ctx.beginPath();
      buf.forEach((s, i) => {
        const x = (BUFFER_SIZE - buf.length + i) * xStep;
        const y = botY - 2 - (s[key] / 100) * usableH;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      // Close the fill path along the bottom
      const lastX = (BUFFER_SIZE - buf.length + buf.length - 1) * xStep;
      const firstX = (BUFFER_SIZE - buf.length) * xStep;
      ctx.lineTo(lastX, botY - 2);
      ctx.lineTo(firstX, botY - 2);
      ctx.closePath();
      ctx.fillStyle = glow;
      ctx.fill();

      // Draw line on top
      ctx.save();
      ctx.shadowBlur = 3;
      ctx.shadowColor = glow;
      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      buf.forEach((s, i) => {
        const x = (BUFFER_SIZE - buf.length + i) * xStep;
        const y = botY - 2 - (s[key] / 100) * usableH;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.stroke();
      ctx.restore();

      ctx.fillStyle = color;
      ctx.font = 'bold 8px "Roboto Condensed", monospace';
      ctx.fillText(label, 3, topY + 9);
    });
  }, []);

  const ingestPayload = useCallback((payload: PayloadEvent) => {
    const p = payload as Record<string, unknown>;
    if (p.sensorType !== SENSOR_TYPE_SYSMON) return false;

    const data = p.sensorData as Record<string, unknown> | undefined;
    if (!data) return false;

    const cpu = (data.cpuUsagePct as number | undefined) ?? 0;
    const memPct = (data.memoryUsedPct as number | undefined) ?? 0;

    bufRef.current = [...bufRef.current, { cpu, mem: memPct }].slice(-BUFFER_SIZE);

    if (!everHadDataRef.current) {
      everHadDataRef.current = true;
      setPanelVisible(true);
    }

    setInfo({
      nodeId: (data.nodeId as string | undefined) ?? '—',
      cpuPct: cpu,
      memUsedMb: Number(data.memoryUsedMb ?? 0),
      memTotalMb: Number(data.memoryTotalMb ?? 0),
      memPct,
      diskUsedGb: Number(data.diskUsedGb ?? 0),
      diskTotalGb: Number(data.diskTotalGb ?? 0),
      diskPct: (data.diskUsedPct as number | undefined) ?? 0,
      loadAvg1m: (data.loadAvg_1M as number | undefined) ?? (data.loadAvg1M as number | undefined) ?? 0,
      uptimeSeconds: (data.uptimeSeconds as number | undefined) ?? 0,
    });

    return true;
  }, []);

  useEffect(() => {
    if (panelVisible) draw();
  }, [panelVisible, draw]);

  useEffect(() => {
    const poll = async () => {
      try {
        const payloads = await beamApi.payloads();
        const newPayloads = payloads
          .filter((p) => {
            const raw = p as Record<string, unknown>;
            if (raw.sensorType !== SENSOR_TYPE_SYSMON) return false;
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

  return (
    <div className="sysmon-panel">
      <div className="sysmon-header">
        <span className="sysmon-title">SYSMON</span>
        <span className="sysmon-node">{info.nodeId}</span>
        <span className="sysmon-stat">
          CPU <span className="sysmon-stat-val">{info.cpuPct.toFixed(1)}%</span>
        </span>
        <span className="sysmon-stat">
          MEM <span className="sysmon-stat-val">{info.memUsedMb}/{info.memTotalMb}MB</span>
        </span>
        <span className="sysmon-stat">
          DISK <span className="sysmon-stat-val">{info.diskUsedGb}/{info.diskTotalGb}GB</span>
        </span>
        <span className="sysmon-stat">
          LOAD <span className="sysmon-stat-val">{info.loadAvg1m.toFixed(2)}</span>
        </span>
        <span className="sysmon-stat">
          UP <span className="sysmon-stat-val">{fmtUptime(info.uptimeSeconds)}</span>
        </span>
        <span className="sysmon-live">● LIVE</span>
        <button className="sysmon-clear-btn" onClick={handleClear}>CLR</button>
      </div>
      <canvas
        ref={canvasRef}
        width={CW}
        height={CANVAS_H}
        className="sysmon-canvas"
      />
    </div>
  );
}
