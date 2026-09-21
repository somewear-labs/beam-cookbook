import { useEffect, useRef, useState, useCallback } from 'react';
import { beamController } from '../controllers/BeamController';
import { WorkspaceInfo, SystemInfo, SatQuality, beamApi } from '../services/beamApi';
import { nodeRepository, NodeRepositoryState } from '../repositories/NodeRepository';
import type { ReconnectPhase } from '../repositories/NodeRepository';

function SatBars({ quality }: { quality: number }) {
  const cls = quality >= 4 ? 'signal-good' : quality >= 2 ? 'signal-fair' : quality > 0 ? 'signal-poor' : 'signal-unknown';
  return (
    <span className={`sat-bars ${cls}`}>
      {[1, 2, 3, 4, 5].map((i) => (
        <span key={i} className={`sat-bar sat-bar-${i}${i <= quality ? ' sat-bar--filled' : ' sat-bar--empty'}`} />
      ))}
    </span>
  );
}

export default function BeamStatus() {
  const [activeWorkspace, setActiveWorkspace] = useState<WorkspaceInfo | null>(
    beamController.activeWorkspace
  );
  const [systemInfo, setSystemInfo] = useState<SystemInfo | null>(beamController.systemInfo);
  const [repoState, setRepoState] = useState<NodeRepositoryState>(nodeRepository.state);
  const [satQuality, setSatQuality] = useState<SatQuality | null>(null);
  const [connecting, setConnecting] = useState(false);
  const connectingRef = useRef(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [flushing, setFlushing] = useState(false);
  const [flushMsg, setFlushMsg] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onWsChange = (ws: unknown) => setActiveWorkspace(ws as WorkspaceInfo | null);
    const onSystemChange = (sys: unknown) => setSystemInfo(sys as SystemInfo | null);

    beamController.on('workspaceChange', onWsChange);
    beamController.on('systemChange', onSystemChange);

    return () => {
      beamController.off('workspaceChange', onWsChange);
      beamController.off('systemChange', onSystemChange);
    };
  }, []);

  useEffect(() => {
    return nodeRepository.subscribe(setRepoState);
  }, []);

  useEffect(() => {
    return beamApi.tailSatelliteQuality(setSatQuality);
  }, []);

  useEffect(() => {
    if (!menuOpen) return;
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [menuOpen]);

  const handleFlushQueue = useCallback(async () => {
    setFlushing(true);
    setMenuOpen(false);
    try {
      const r = await beamApi.flushQueue();
      setFlushMsg(`FLUSHED ${r.flushed}/${r.total}`);
    } catch {
      setFlushMsg('FLUSH FAILED');
    } finally {
      setFlushing(false);
      setTimeout(() => setFlushMsg(null), 3000);
    }
  }, []);

  const { networkState, primaryDevice, reconnectPhase, cooldownSecondsRemaining } = repoState;

  const nodeStatus = (): { label: string; cls: string } => {
    if (reconnectPhase === 'reconnecting') return { label: 'RECONNECTING...', cls: 'status-warning' };
    if (!networkState) return { label: '—', cls: 'status-muted' };
    const s = networkState.connectionState;
    if (s === 'Connected') return { label: 'CONNECTED', cls: 'status-connected' };
    if (s === 'Connecting' || s === 'Scanning') return { label: s.toUpperCase(), cls: 'status-warning' };
    return { label: s.toUpperCase(), cls: 'status-error' };
  };

  const { label: nodeLabel, cls: nodeCls } = nodeStatus();
  const nodeVersion = systemInfo?.version?.current ?? null;
  const isConnected = networkState?.connectionState === 'Connected';
  // During auto-reconnect the UI owns the connect flow — hide the manual button.
  const autoReconnectActive = reconnectPhase !== 'idle';

  async function handleConnect() {
    if (connectingRef.current) return;
    connectingRef.current = true;
    setConnecting(true);
    try {
      await beamApi.deviceConnect();
    } catch (err) {
      console.warn('[BeamStatus] deviceConnect failed:', err);
    } finally {
      connectingRef.current = false;
      setConnecting(false);
    }
  }

  return (
    <div className="tactical-status-panel">
      <div className="status-panel-header beam-node-header">
        <span>BEAM NODE</span>
        <div className="beam-node-menu" ref={menuRef}>
          <button
            className="beam-node-menu-btn"
            onClick={() => setMenuOpen((o) => !o)}
            title="Device options"
          >···</button>
          {menuOpen && (
            <div className="beam-node-menu-dropdown">
              <button
                className="beam-node-menu-item"
                onClick={handleFlushQueue}
                disabled={flushing}
              >{flushing ? 'FLUSHING…' : 'FLUSH QUEUE'}</button>
            </div>
          )}
        </div>
      </div>
      {flushMsg && <div className="beam-node-flush-msg">{flushMsg}</div>}

      <div className="status-row">
        <span className="status-label">STATUS</span>
        <span className={`status-value ${nodeCls}`}>{nodeLabel}</span>
      </div>

      {/* Auto-reconnect status block — shown instead of the manual connect button */}
      {autoReconnectActive && (
        <div className="reconnect-status-block">
          {reconnectPhase === 'cooldown' && (
            <>
              <div className="reconnect-status-line reconnect-status-line--warn">
                NODE DISCONNECTED
              </div>
              <div className="reconnect-status-line reconnect-status-line--muted">
                Waiting {cooldownSecondsRemaining}s for node to recover
              </div>
              <div className="reconnect-status-line reconnect-status-line--muted">
                Auto-reconnect in {cooldownSecondsRemaining}s…
              </div>
            </>
          )}
          {reconnectPhase === 'reconnecting' && (
            <>
              <div className="reconnect-status-line reconnect-status-line--warn">
                RECONNECTING TO NODE
              </div>
              <div className="reconnect-status-line reconnect-status-line--muted">
                Cooldown complete — attempting connection
              </div>
            </>
          )}
        </div>
      )}

      {!isConnected && !autoReconnectActive && (
        <button
          className={`node-connect-btn${connecting ? ' node-connect-btn--busy' : ''}`}
          onClick={handleConnect}
          disabled={connecting}
        >
          {connecting ? 'CONNECTING...' : 'CONNECT'}
        </button>
      )}

      <div className="status-row">
        <span className="status-label">SAT</span>
        <span className="status-value">
          {networkState?.connectionState === 'Connected'
            ? <SatBars quality={satQuality?.quality ?? 0} />
            : <span className="status-muted">—</span>
          }
        </span>
      </div>

      {networkState?.serial && (
        <div className="status-row">
          <span className="status-label">SERIAL</span>
          <span className="status-value status-muted">{networkState.serial}</span>
        </div>
      )}

      {nodeVersion && (
        <div className="status-row">
          <span className="status-label">VERSION</span>
          <span className="status-value status-muted">v{nodeVersion}</span>
        </div>
      )}

      {primaryDevice && (
        <>
          {primaryDevice.firmwareVersion && (
            <div className="status-row">
              <span className="status-label">FW</span>
              <span className="status-value status-muted">{primaryDevice.firmwareVersion}</span>
            </div>
          )}
          <div className="status-row">
            <span className="status-label">BATTERY</span>
            <span className={`status-value ${primaryDevice.battery > 20 ? 'status-connected' : 'status-error'}`}>
              {primaryDevice.battery}%
            </span>
          </div>
        </>
      )}

      <div className="status-divider" />

      <div className="status-row">
        <span className="status-label">WORKSPACE</span>
        <span className="status-value">
          {activeWorkspace ? activeWorkspace.name : '—'}
        </span>
      </div>

      <div className="status-row">
        <span className="status-label">WS ID</span>
        <span className="status-value status-muted">
          {activeWorkspace ? activeWorkspace.workspaceId : '—'}
        </span>
      </div>

    </div>
  );
}
