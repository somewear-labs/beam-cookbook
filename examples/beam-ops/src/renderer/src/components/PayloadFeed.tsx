import { useEffect, useRef, useState } from 'react';
import { beamController } from '../controllers/BeamController';
import { beamApi, PayloadEvent } from '../services/beamApi';
import QueueVisualizer from './QueueVisualizer';

type PanelTab = 'feed' | 'queue';

const MAX_PAYLOADS = 50;
const DEFAULT_WIDTH = 420;
const MIN_WIDTH = 240;
const MAX_WIDTH = 700;

function formatTimestamp(ts?: number): string {
  if (!ts) return '--:--:--';
  const d = new Date(typeof ts === 'number' && ts < 1e12 ? ts * 1000 : ts);
  return d.toLocaleTimeString('en-US', { hour12: false, timeZone: 'UTC' }) + 'Z';
}

function formatPayloadType(type?: string): string {
  if (!type) return 'PKT';
  return type.toUpperCase().slice(0, 8);
}

function ChannelIcon({ channel }: { channel?: string | null }) {
  if (!channel) return <span className="payload-channel" />;
  const ch = channel.toLowerCase();
  if (ch === 'satellite') return (
    <span className="payload-channel payload-channel--sat" title="Satellite">
      <svg viewBox="0 0 19 22" width="11" height="13" fill="none">
        <path d="M6.36,11.709C6.841,13.282 8.506,14.167 10.079,13.687C11.652,13.206 12.537,11.541 12.056,9.968" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round"/>
        <path d="M4.084,13.101C4.997,16.09 8.161,17.772 11.149,16.858C14.138,15.944 15.82,12.781 14.906,9.792" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round"/>
        <path d="M6.389,2.132L5.824,2.336A0.915,0.915 115.177,0 0,5.274 3.507L6.193,6.056A0.915,0.915 115.177,0 0,7.363 6.606L7.929,6.402A0.915,0.915 115.177,0 0,8.479 5.232L7.56,2.683A0.915,0.915 115.177,0 0,6.389 2.132z" fill="currentColor"/>
        <path d="M5.125,4.076L1.777,5.283A0.059,0.059 115.177,0 0,1.741 5.358L2.549,7.599A0.059,0.059 115.177,0 0,2.625 7.635L5.973,6.428A0.059,0.059 115.177,0 0,6.008 6.352L5.2,4.111A0.059,0.059 115.177,0 0,5.125 4.076z" fill="currentColor"/>
        <path d="M11.39,1.834L8.043,3.04A0.059,0.059 115.177,0 0,8.008 3.116L8.815,5.355A0.059,0.059 115.177,0 0,8.89 5.39L12.237,4.184A0.059,0.059 115.177,0 0,12.273 4.108L11.466,1.869A0.059,0.059 115.177,0 0,11.39 1.834z" fill="currentColor"/>
        <path d="M8.684,7.588l-0.702,0.253l0.705,1.956l0.702,-0.253z" fill="currentColor"/>
        <path d="M6.974,4.301A0.464,0.464 71.333,0 0,6.695 4.894L8.525,9.971A0.464,0.464 71.333,0 0,9.119 10.25A0.464,0.464 71.333,0 0,9.398 9.656L7.568,4.58A0.464,0.464 71.333,0 0,6.974 4.301z" fill="currentColor"/>
        <path d="M5.91,10.068C5.371,8.573 6.161,6.919 7.673,6.374C9.185,5.829 10.848,6.599 11.387,8.094" fill="currentColor" fillRule="evenodd"/>
      </svg>
    </span>
  );
  if (ch === 'cellular') return (
    <span className="payload-channel payload-channel--cell" title="Cellular">
      <svg viewBox="0 0 12 12" width="12" height="12" fill="currentColor">
        <path d="M2.238,8.762C1.713,8.262 1.317,7.677 1.05,7.006C0.783,6.335 0.65,5.667 0.65,5C0.65,4.333 0.779,3.669 1.038,3.006C1.296,2.344 1.696,1.75 2.238,1.225L2.838,1.837C2.404,2.262 2.073,2.758 1.844,3.325C1.615,3.892 1.5,4.45 1.5,5C1.5,5.55 1.615,6.108 1.844,6.675C2.073,7.242 2.404,7.737 2.838,8.162L2.238,8.762ZM3.513,7.487C3.154,7.154 2.894,6.771 2.731,6.337C2.569,5.904 2.488,5.458 2.488,5C2.488,4.592 2.565,4.162 2.719,3.712C2.873,3.262 3.138,2.862 3.513,2.512L4.113,3.112C3.871,3.362 3.683,3.665 3.55,4.019C3.417,4.373 3.35,4.7 3.35,5C3.35,5.283 3.415,5.602 3.544,5.956C3.673,6.31 3.863,6.621 4.113,6.887L3.513,7.487ZM3.538,10.925L5.263,6.175C5.096,6.067 4.952,5.912 4.831,5.712C4.71,5.512 4.65,5.275 4.65,5C4.65,4.617 4.779,4.296 5.038,4.037C5.296,3.779 5.617,3.65 6,3.65C6.383,3.65 6.704,3.779 6.963,4.037C7.221,4.296 7.35,4.617 7.35,5C7.35,5.275 7.29,5.508 7.169,5.7C7.048,5.892 6.904,6.05 6.738,6.175L8.463,10.925H7.513L7.125,9.8H4.888L4.488,10.925H3.538ZM5.163,9.05H6.838L6,6.612L5.163,9.05ZM8.488,7.487L7.888,6.887C8.138,6.637 8.327,6.335 8.456,5.981C8.585,5.627 8.65,5.3 8.65,5C8.65,4.717 8.585,4.398 8.456,4.044C8.327,3.69 8.138,3.379 7.888,3.112L8.488,2.512C8.854,2.862 9.119,3.262 9.281,3.712C9.444,4.162 9.521,4.592 9.513,5C9.513,5.4 9.433,5.827 9.275,6.281C9.117,6.735 8.854,7.137 8.488,7.487ZM9.775,8.762L9.163,8.162C9.588,7.737 9.919,7.242 10.156,6.675C10.394,6.108 10.512,5.55 10.512,5C10.512,4.45 10.394,3.892 10.156,3.325C9.919,2.758 9.588,2.262 9.163,1.837L9.775,1.225C10.308,1.75 10.706,2.344 10.969,3.006C11.231,3.669 11.363,4.333 11.363,5C11.363,5.667 11.238,6.331 10.988,6.994C10.738,7.656 10.333,8.246 9.775,8.762Z"/>
      </svg>
    </span>
  );
  if (ch === 'radio') return (
    <span className="payload-channel payload-channel--radio" title="Radio">
      <svg viewBox="0 0 22 22" width="12" height="12" fill="currentColor">
        <path d="M9.417,16.833L3,10.417L9.417,4V16.833ZM8.156,13.785V7.048L4.788,10.417L8.156,13.785ZM13.083,16.833V4L19.5,10.417L13.083,16.833Z"/>
      </svg>
    </span>
  );
  return <span className="payload-channel" title={channel} />;
}

function n(v: unknown, dec = 1, unit = ''): string {
  return `${Number(v ?? 0).toFixed(dec)}${unit}`;
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return d > 0 ? `${d}d ${h}h ${m}m` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

type DetailRow = { label: string; value: string };

function sensorRows(name: string, d: Record<string, unknown>): DetailRow[] {
  const s = (v: unknown) => (v != null ? String(v) : '—');
  switch (name) {
    case 'SYSMON': {
      const cores = Array.isArray(d.perCorePct)
        ? (d.perCorePct as number[]).map((c) => `${c.toFixed(0)}%`).join(' ')
        : '';
      return [
        { label: 'NODE', value: s(d.nodeId) },
        { label: 'UPTIME', value: d.uptimeSeconds != null ? formatUptime(Number(d.uptimeSeconds)) : '—' },
        { label: 'CPU', value: `${n(d.cpuUsagePct)}%  load ${n(d.loadAvg1m, 2)} / ${n(d.loadAvg5m, 2)} / ${n(d.loadAvg15m, 2)}` },
        ...(cores ? [{ label: 'CORES', value: cores }] : []),
        { label: 'MEMORY', value: `${n(d.memoryUsedMb, 0)} / ${n(d.memoryTotalMb, 0)} MB  (${n(d.memoryUsedPct)}%)` },
        { label: 'DISK', value: `${n(d.diskUsedGb, 1)} / ${n(d.diskTotalGb, 1)} GB  (${n(d.diskUsedPct)}%)` },
      ];
    }
    case 'CAMERA': {
      const dets = Array.isArray(d.detections) ? (d.detections as Record<string, unknown>[]) : [];
      return [
        { label: 'CAMERA', value: s(d.cameraId) },
        { label: 'FRAME', value: s(d.frameNumber) },
        { label: 'INFER', value: d.inferenceMs != null ? `${d.inferenceMs}ms` : '—' },
        ...(d.streamUrl ? [{ label: 'URL', value: s(d.streamUrl) }] : []),
        ...dets.map((det, i) => ({
          label: `DET ${i + 1}`,
          value: `${s(det.className)}  ${(Number(det.confidence) * 100).toFixed(1)}%  cx=${n(det.bboxCx, 3)} cy=${n(det.bboxCy, 3)} w=${n(det.bboxW, 3)} h=${n(det.bboxH, 3)}`,
        })),
      ];
    }
    case 'UGS': {
      const threat = String(d.threatType ?? 'NONE');
      const hasLoc = d.latitude != null && Number(d.latitude) !== 0;
      return [
        { label: 'NODE', value: s(d.nodeId) },
        ...(hasLoc ? [{ label: 'POS', value: `${n(d.latitude, 5)}°  ${n(d.longitude, 5)}°  alt ${n(d.altitudeM, 0)}m` }] : []),
        { label: 'THREAT', value: `${threat}${threat !== 'NONE' ? `  ${d.confidencePct ?? '?'}%` : ''}` },
        ...(threat !== 'NONE' ? [{ label: 'BEARING', value: `${n(d.bearingDeg, 1)}°  range ${n(d.estimatedRangeM, 0)}m` }] : []),
        { label: 'PEAK AMP', value: `${n(d.peakAmplitude, 4)} m/s²` },
        { label: 'SAMPLE', value: `${s(d.sampleRateHz)} Hz` },
        ...(d.eventId ? [{ label: 'EVENT', value: s(d.eventId) }] : []),
      ];
    }
    case 'CBRN': {
      const agent = String(d.chemAgent ?? 'NONE');
      return [
        { label: 'NODE', value: s(d.nodeId) },
        { label: 'THREAT', value: s(d.threatLevel) },
        { label: 'ALARM', value: d.alarmActive ? 'ACTIVE' : 'none' },
        { label: 'RADIATION', value: `${n(d.radiationMradHr, 3)} mRad/hr` },
        { label: 'CHEM AGENT', value: `${agent}${agent !== 'NONE' ? `  ${n(d.chemConcentPpb, 1)} ppb` : ''}` },
        { label: 'BIO', value: d.bioIndicator ? 'DETECTED' : 'clear' },
        { label: 'TOX IND', value: d.toxIndustrial ? 'DETECTED' : 'clear' },
        { label: 'ENV', value: `${n(d.temperatureC, 1)}°C  RH ${n(d.humidityPct)}%  wind ${n(d.windDirDeg, 0)}°` },
        ...(d.eventId ? [{ label: 'EVENT', value: s(d.eventId) }] : []),
      ];
    }
    case 'TWS':
      return [
        { label: 'STATION', value: s(d.stationId) },
        { label: 'CATEGORY', value: s(d.flightCategory) },
        { label: 'TEMP', value: `${n(d.temperatureC, 1)}°C  dew ${n(d.dewPointC, 1)}°C  RH ${n(d.humidityPct)}%` },
        { label: 'PRESSURE', value: `${n(d.pressureHpa, 1)} hPa  /  ${n(d.altimeterInhg, 2)} inHg` },
        { label: 'WIND', value: `${n(d.windSpeedKts, 0)} kts @ ${n(d.windDirectionDeg, 0)}°  gust ${n(d.gustKts, 0)} kts  xwind ${n(d.crosswindKts, 1)} kts` },
        { label: 'VISIBILITY', value: `${n(d.visibilityKm, 1)} km  ceil ${s(d.ceilingFt)} ft` },
        { label: 'DENSITY ALT', value: `${s(d.densityAltitudeFt)} ft` },
        ...(d.dustStormWarning ? [{ label: 'WARNING', value: 'DUST STORM' }] : []),
        ...(d.operationalNote ? [{ label: 'NOTE', value: s(d.operationalNote) }] : []),
      ];
    case 'VIMU':
      return [
        { label: 'VEHICLE', value: `${s(d.vehicleId)}  unit ${s(d.unitId)}` },
        { label: 'STATE', value: s(d.motionState) },
        { label: 'NAV', value: `hdg ${n(d.headingDeg, 0)}°  pitch ${n(d.pitchDeg, 1)}°  roll ${n(d.rollDeg, 1)}°` },
        { label: 'SPEED', value: `${n(d.speedMps, 1)} m/s  G-peak ${n(d.gForcePeak, 2)}g` },
        { label: 'ACCEL', value: `x=${n(d.accelX, 3)}  y=${n(d.accelY, 3)}  z=${n(d.accelZ, 3)} m/s²` },
        { label: 'GYRO', value: `x=${n(d.gyroX, 3)}  y=${n(d.gyroY, 3)}  z=${n(d.gyroZ, 3)} rad/s` },
        { label: 'TEMP', value: `${n(d.temperatureC, 1)}°C` },
      ];
    case 'SEISMOGRAPH':
      return [
        { label: 'STATION', value: `${s(d.stationId)}  net ${s(d.networkCode)}` },
        { label: 'MAGNITUDE', value: `M${n(d.magnitude, 1)}` },
        { label: 'DEPTH', value: `${n(d.depthKm, 1)} km` },
        { label: 'EPICENTER', value: `${n(d.epicenterKm, 1)} km` },
        { label: 'INTENSITY', value: s(d.intensity) },
        { label: 'PGV', value: `${n(d.pgvMs, 5)} m/s` },
        { label: 'SAMPLE', value: `${s(d.sampleRateHz)} Hz` },
        ...(d.eventId ? [{ label: 'EVENT', value: s(d.eventId) }] : []),
      ];
    default:
      return Object.entries(d).map(([k, v]) => ({ label: k.toUpperCase(), value: s(v) }));
  }
}

function SensorDetail({ sensorName, sensorData }: { sensorName: string; sensorData: Record<string, unknown> }) {
  const rows = sensorRows(sensorName, sensorData);
  return (
    <div className="sensor-detail">
      <div className="sensor-detail-header">{sensorName}</div>
      {rows.map(({ label, value }) => (
        <div key={label} className="sensor-detail-row">
          <span className="sensor-detail-label">{label}</span>
          <span className="sensor-detail-value">{value}</span>
        </div>
      ))}
    </div>
  );
}

function formatContent(payload: PayloadEvent): string {
  const sensorName = payload.sensorName as string | undefined;
  const sensorData = payload.sensorData as Record<string, unknown> | undefined;
  if (sensorName && sensorData) {
    switch (sensorName) {
      case 'SYSMON':
        return `CPU ${n(sensorData.cpuUsagePct)}% | MEM ${n(sensorData.memoryUsedPct, 0)}% | DISK ${n(sensorData.diskUsedPct, 0)}%`;
      case 'CAMERA': {
        const dets = Array.isArray(sensorData.detections) ? sensorData.detections as Record<string, unknown>[] : [];
        if (dets.length === 0) return 'No detections';
        const top = dets.slice(0, 2).map((d) => `${d.className} ${(Number(d.confidence) * 100).toFixed(0)}%`).join(', ');
        return `${dets.length} detection${dets.length !== 1 ? 's' : ''}: ${top}`;
      }
      case 'UGS': {
        const threat = String(sensorData.threatType ?? 'NONE');
        if (threat !== 'NONE') return `${threat} ${sensorData.confidencePct ?? '?'}% @ ${n(sensorData.bearingDeg, 0)}° ${n(sensorData.estimatedRangeM, 0)}m`;
        return `UGS @ ${n(sensorData.latitude, 4)}, ${n(sensorData.longitude, 4)}`;
      }
      case 'CBRN': {
        const level = String(sensorData.threatLevel ?? '');
        const agent = String(sensorData.chemAgent ?? 'NONE');
        const alarm = sensorData.alarmActive ? ' ALARM' : '';
        return `${level}${alarm}${agent !== 'NONE' ? ` ${agent} ${n(sensorData.chemConcentPpb, 1)}ppb` : ''}`;
      }
      case 'TWS':
        return `${n(sensorData.temperatureC, 1)}°C W${n(sensorData.windSpeedKts, 0)}kts/${n(sensorData.windDirectionDeg, 0)}° ${sensorData.flightCategory ?? ''}`;
      case 'VIMU':
        return `${sensorData.motionState ?? ''} hdg=${n(sensorData.headingDeg, 0)}° ${n(sensorData.speedMps, 1)}m/s`;
      case 'SEISMOGRAPH':
        return `M${n(sensorData.magnitude, 1)} depth=${n(sensorData.depthKm, 1)}km`;
      default:
        return sensorName;
    }
  }
  const c = payload.content;
  if (c && typeof c === 'object') {
    const obj = c as Record<string, unknown>;
    if (typeof obj.text === 'string' && obj.text) return obj.text;
    if (obj.latitude !== undefined) return `${Number(obj.latitude).toFixed(5)}, ${Number(obj.longitude).toFixed(5)}`;
    if (typeof obj.sosType === 'string') return `SOS: ${obj.sosType}`;
    if (typeof obj.rawCot === 'string') return obj.rawCot.slice(0, 80);
    if (typeof obj.name === 'string') return obj.name;
    return JSON.stringify(c);
  }
  if (c) return String(c);
  const { id: _id, timestamp: _ts, type: _type, content: _c, senderId: _s, ...rest } = payload;
  const keys = Object.keys(rest);
  if (keys.length > 0) {
    return JSON.stringify(rest);
  }
  return '[empty]';
}

export default function PayloadFeed({ workspaceId }: { workspaceId: string | null }) {
  const [tab, setTab] = useState<PanelTab>('feed');
  const [payloads, setPayloads] = useState<PayloadEvent[]>([]);
  const [filter, setFilter] = useState('');
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const cleanupRef = useRef<(() => void) | null>(null);
  const resizeDragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  useEffect(() => {
    beamApi
      .payloads()
      .then((recent) => {
        const sorted = [...recent].sort((a, b) => {
          const ta = a.timestamp ? new Date(a.timestamp as string).getTime() : 0;
          const tb = b.timestamp ? new Date(b.timestamp as string).getTime() : 0;
          return tb - ta;
        });
        setPayloads(sorted.slice(0, MAX_PAYLOADS));
      })
      .catch((err) => {
        console.warn('[PayloadFeed] Failed to load recent payloads:', err);
      });
  }, []);

  useEffect(() => {
    if (beamController.connectionState !== 'connected') return;
    cleanupRef.current = beamController.tailPayloads((payload) => {
      setPayloads((prev) => {
        const key = `${payload.datagramId}_${payload.channel}`;
        if (payload.datagramId && payload.channel && prev.some(p => `${p.datagramId}_${p.channel}` === key)) return prev;
        return [payload, ...prev].slice(0, MAX_PAYLOADS);
      });
    });
    return () => {
      cleanupRef.current?.();
      cleanupRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (!filter && listRef.current) {
      listRef.current.scrollTop = 0;
    }
  }, [payloads.length, filter]);

  useEffect(() => {
    const onMouseMove = (e: MouseEvent) => {
      if (!resizeDragRef.current) return;
      const delta = resizeDragRef.current.startX - e.clientX;
      const newWidth = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, resizeDragRef.current.startWidth + delta));
      setWidth(newWidth);
    };
    const onMouseUp = () => {
      if (!resizeDragRef.current) return;
      resizeDragRef.current = null;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
    return () => {
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
    };
  }, []);

  const workspaceFiltered = payloads
    .filter(p => String(p.channel ?? '').toLowerCase() !== 'none')
    .filter(p => !workspaceId || !p.workspaceId || String(p.workspaceId) === workspaceId);

  const filteredPayloads = filter.trim()
    ? workspaceFiltered.filter((p) => {
        const q = filter.toLowerCase();
        return (
          formatPayloadType(p.type).toLowerCase().includes(q) ||
          (p.sourceUserId && String(p.sourceUserId).toLowerCase().includes(q)) ||
          (p.datagramId && String(p.datagramId).toLowerCase().includes(q)) ||
          formatContent(p).toLowerCase().includes(q)
        );
      })
    : workspaceFiltered;

  return (
    <div className="payload-feed" style={{ width }}>
      <div
        className="payload-resize-handle"
        onMouseDown={(e) => {
          resizeDragRef.current = { startX: e.clientX, startWidth: width };
          document.body.style.cursor = 'ew-resize';
          document.body.style.userSelect = 'none';
          e.preventDefault();
        }}
      />

      {/* Top-level panel tabs */}
      <div className="panel-tabs">
        <button
          className={`panel-tab${tab === 'feed' ? ' panel-tab--active' : ''}`}
          onClick={() => setTab('feed')}
        >
          INBOUND
        </button>
        <button
          className={`panel-tab${tab === 'queue' ? ' panel-tab--active' : ''}`}
          onClick={() => setTab('queue')}
        >
          OUTBOUND
        </button>
      </div>

      {tab === 'feed' && (
        <>
          <div className="payload-filter-bar">
            <input
              className="payload-filter-input"
              type="text"
              placeholder="FILTER..."
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              spellCheck={false}
            />
            {filter && (
              <button className="payload-filter-clear" onClick={() => setFilter('')}>
                ✕
              </button>
            )}
          </div>

          <div className="payload-feed-list" ref={listRef}>
            {filteredPayloads.length === 0 ? (
              <div className="payload-empty">{filter ? 'NO MATCHES' : 'NO PAYLOADS'}</div>
            ) : (
              filteredPayloads.map((p, idx) => {
                const key = p.id ?? String(idx);
                const expanded = expandedKey === key;
                return (
                  <div key={key} className={`payload-item${expanded ? ' payload-item--expanded' : ''}`} onClick={() => setExpandedKey(expanded ? null : key)}>
                    <span className="payload-time">{formatTimestamp(p.timestamp)}</span>
                    <span className="payload-type">{formatPayloadType(p.type)}</span>
                    <span className="payload-sender">
                      {p.sourceUserId ? String(p.sourceUserId).slice(0, 8) : '--'}
                    </span>
                    <ChannelIcon channel={p.channel} />
                    <span className="payload-content">{formatContent(p)}</span>
                    {p.datagramId && (
                      <span className="payload-datagram">{p.datagramId}</span>
                    )}
                    {expanded && (
                      p.sensorName && p.sensorData
                        ? <div className="payload-detail"><SensorDetail sensorName={String(p.sensorName)} sensorData={p.sensorData as Record<string, unknown>} /></div>
                        : <pre className="payload-detail">{JSON.stringify(p, null, 2)}</pre>
                    )}
                  </div>
                );
              })
            )}
          </div>
        </>
      )}

      {tab === 'queue' && <QueueVisualizer />}
    </div>
  );
}
