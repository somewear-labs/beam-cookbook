import { useCallback, useEffect, useRef, useState } from 'react';

interface LogLine {
  id: number;
  text: string;
  kind: 'output' | 'meta';
}

interface WorkspaceOption {
  workspaceId: string;
  name: string;
}

interface BeamNodeInfo {
  authState: string;
  deviceSerial: string | null;
  connectionState: string;
  workspaceName: string | null;
  workspaceId: string | null;
}

let seq = 0;
const mkLine = (kind: LogLine['kind'], text: string): LogLine => ({ id: seq++, kind, text });

export default function EdgeComputePanel() {
  const [port, setPort] = useState('8080');
  const [workspaceId, setWorkspaceId] = useState('');
  const [workspaces, setWorkspaces] = useState<WorkspaceOption[]>([]);
  const [running, setRunning] = useState(false);
  const [lines, setLines] = useState<LogLine[]>([]);
  const [ready, setReady] = useState(false);
  const [nodeInfo, setNodeInfo] = useState<BeamNodeInfo | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    Promise.all([
      window.edgeApi.loadConfig(),
      window.edgeApi.status(),
      window.beamApi.workspaces().catch(() => ({ workspaces: [], activeWorkspaceId: null })),
      window.beamApi.authState().catch(() => null),
      window.beamApi.network().catch(() => null),
    ]).then(([cfg, { running: isRunning }, wsResp, auth, net]) => {
      setPort(String(cfg.port));
      const opts = wsResp.workspaces.map(w => ({ workspaceId: w.workspaceId, name: w.name }));
      setWorkspaces(opts);
      const activeWsId = cfg.workspaceId
        ? String(cfg.workspaceId)
        : wsResp.activeWorkspaceId ?? (opts[0]?.workspaceId ?? '');
      setWorkspaceId(activeWsId);
      const activeWs = opts.find(w => w.workspaceId === activeWsId) ?? opts[0] ?? null;
      setNodeInfo({
        authState: auth?.state ?? 'Unknown',
        deviceSerial: net?.serial ?? null,
        connectionState: net?.connectionState ?? 'Unknown',
        workspaceName: activeWs?.name ?? null,
        workspaceId: activeWsId || null,
      });
      setRunning(isRunning);
      setReady(true);
    });

    const offOutput = window.edgeApi.onOutput((line) =>
      setLines((prev) => [...prev, mkLine('output', line)])
    );
    const offExit = window.edgeApi.onExit((code) => {
      setRunning(false);
      setLines((prev) => [...prev, mkLine('meta', `server exited (${code ?? '?'})`)]);
    });
    const offError = window.edgeApi.onError((msg) => {
      setRunning(false);
      setLines((prev) => [...prev, mkLine('meta', `error: ${msg}`)]);
    });

    return () => { offOutput(); offExit(); offError(); };
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'nearest' });
  }, [lines]);

  const handleStart = useCallback(async () => {
    const portNum = parseInt(port, 10);
    const wsNum = parseInt(workspaceId, 10);
    if (isNaN(portNum) || isNaN(wsNum) || wsNum === 0) return;

    setLines([mkLine('meta', `starting  rpc server --port ${portNum} --workspace ${wsNum}`)]);
    try {
      await window.edgeApi.start(portNum, wsNum);
      setRunning(true);
    } catch (err) {
      setLines((prev) => [
        ...prev,
        mkLine('meta', `failed: ${err instanceof Error ? err.message : String(err)}`)
      ]);
    }
  }, [port, workspaceId]);

  const handleStop = useCallback(async () => {
    await window.edgeApi.stop();
    setRunning(false);
    setLines((prev) => [...prev, mkLine('meta', 'server stopped')]);
  }, []);

  const canStart = ready && !!port && !!workspaceId && parseInt(workspaceId, 10) > 0;

  return (
    <div className="tactical-status-panel edge-compute-panel">
      <div className="status-panel-header edge-compute-header">
        <span>EDGE COMPUTE</span>
        <span className={`nodes-live-badge ${running ? 'live' : 'offline'}`}>
          {running ? '● RUNNING' : '○ STOPPED'}
        </span>
      </div>

      {nodeInfo && (
        <div className="edge-node-info">
          <div className="edge-node-row">
            <span className="edge-node-label">STATUS</span>
            <span className={`edge-node-value ${nodeInfo.authState === 'SignedIn' ? 'edge-node-ok' : 'edge-node-warn'}`}>
              {nodeInfo.authState}
            </span>
          </div>
          <div className="edge-node-row">
            <span className="edge-node-label">DEVICE</span>
            <span className="edge-node-value">
              {nodeInfo.deviceSerial ?? '—'}
              <span className={`edge-node-conn ${nodeInfo.connectionState === 'Connected' ? 'edge-node-ok' : 'edge-node-warn'}`}>
                {' '}· {nodeInfo.connectionState}
              </span>
            </span>
          </div>
          <div className="edge-node-row">
            <span className="edge-node-label">WORKSPACE</span>
            <span className="edge-node-value">
              {nodeInfo.workspaceId ?? '—'}
              {nodeInfo.workspaceName && <span className="edge-node-ws-name"> · {nodeInfo.workspaceName}</span>}
            </span>
          </div>
        </div>
      )}

      <div className="edge-config">
        <div className="edge-config-row">
          <label className="edge-config-label">WORKSPACE</label>
          {workspaces.length > 0 ? (
            <select
              className="edge-config-select"
              value={workspaceId}
              onChange={(e) => {
                const id = e.target.value;
                setWorkspaceId(id);
                window.beamApi.activateWorkspace(id).catch(() => {});
              }}
              disabled={running || !ready}
            >
              {workspaces.map(w => (
                <option key={w.workspaceId} value={w.workspaceId}>{w.name}</option>
              ))}
            </select>
          ) : (
            <input
              className="edge-config-input"
              value={workspaceId}
              onChange={(e) => setWorkspaceId(e.target.value)}
              disabled={running || !ready}
              placeholder="workspace id"
              spellCheck={false}
            />
          )}
        </div>
        <div className="edge-config-row">
          <label className="edge-config-label">PORT</label>
          <input
            className="edge-config-input"
            value={port}
            onChange={(e) => setPort(e.target.value)}
            disabled={running || !ready}
            placeholder="8080"
            spellCheck={false}
          />
        </div>
        <button
          className={`edge-start-btn${running ? ' edge-start-btn--stop' : ''}`}
          onClick={running ? handleStop : handleStart}
          disabled={!ready || (!running && !canStart)}
        >
          {running ? 'Stop Server' : 'Start Server'}
        </button>
      </div>

      <div className="edge-log">
        {lines.length === 0 ? (
          <div className="edge-log-line edge-log-meta">server not running</div>
        ) : (
          lines.map((l) => (
            <div key={l.id} className={`edge-log-line edge-log-${l.kind}`}>
              {l.text}
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
