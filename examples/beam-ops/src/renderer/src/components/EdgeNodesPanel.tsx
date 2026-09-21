import { useState, useRef, useCallback, KeyboardEvent, useEffect } from 'react';
import maplibregl from 'maplibre-gl';
import { beamApi, IdentityDTO } from '../services/beamApi';
import { useShell, NodeSysInfo } from '../hooks/useShell';

interface EdgeNode {
  id: string;
  identity: IdentityDTO;
  sysInfo?: NodeSysInfo;
}

interface EdgeNodesPanelProps {
  workspaceId: string | null;
  map: maplibregl.Map | null;
  onNodeFocus: (lng: number, lat: number) => void;
}

const MOCK_LNG = -121.51119814096896;
const MOCK_LAT = 45.70607863026713;

function createMarkerElement(label: string): HTMLElement {
  const el = document.createElement('div');
  el.className = 'edge-node-marker';
  el.innerHTML = `
    <div class="edge-node-marker-icon">
      <div class="edge-node-marker-ring"></div>
      <div class="edge-node-marker-dot"></div>
    </div>
    <div class="edge-node-marker-label">${label}</div>
  `;
  return el;
}

export default function EdgeNodesPanel({ workspaceId, map, onNodeFocus }: EdgeNodesPanelProps) {
  const [nodes, setNodes] = useState<EdgeNode[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [identities, setIdentities] = useState<IdentityDTO[] | null>(null);
  const [loadingIds, setLoadingIds] = useState(false);
  const [activeNodeId, setActiveNodeId] = useState<string | null>(null);

  const shell = useShell();
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const markersRef = useRef<Map<string, maplibregl.Marker>>(new Map());

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'nearest' });
  }, [shell.lines]);

  // Re-focus input when a command finishes (elapsed goes null while still running)
  useEffect(() => {
    if (shell.running && shell.elapsed === null) {
      inputRef.current?.focus();
    }
  }, [shell.elapsed, shell.running]);

  // Sync HTML markers to the nodes list
  useEffect(() => {
    if (!map) return;
    const current = markersRef.current;
    const activeIds = new Set(nodes.map(n => n.id));

    // Remove markers for nodes that are gone
    for (const [id, marker] of current) {
      if (!activeIds.has(id)) { marker.remove(); current.delete(id); }
    }
    // Add markers for new nodes
    for (const node of nodes) {
      if (!current.has(node.id)) {
        const marker = new maplibregl.Marker({ element: createMarkerElement(node.identity.name || node.identity.email), anchor: 'center' })
          .setLngLat([MOCK_LNG, MOCK_LAT])
          .addTo(map);
        current.set(node.id, marker);
      }
    }
  }, [map, nodes]);

  // Remove all markers on unmount
  useEffect(() => () => {
    for (const m of markersRef.current.values()) m.remove();
    markersRef.current.clear();
  }, []);

  const openPicker = async () => {
    setPickerOpen(true);
    if (identities !== null) return;
    setLoadingIds(true);
    try {
      const result = await beamApi.identities();
      const list = Array.isArray(result) ? result : ((result as Record<string, unknown>).identities as IdentityDTO[] | undefined) ?? [];
      setIdentities(list);
    } catch {
      setIdentities([]);
    } finally {
      setLoadingIds(false);
    }
  };

  const addNode = (identity: IdentityDTO) => {
    const id = `node-${identity.userAccountId}-${Date.now()}`;
    setNodes(prev => [...prev, { id, identity }]);
    setPickerOpen(false);
  };

  const closeTerminal = useCallback(() => {
    shell.disconnect();
    setActiveNodeId(null);
  }, [shell.disconnect]);

  const removeNode = (nodeId: string) => {
    if (activeNodeId === nodeId) closeTerminal();
    setNodes(prev => prev.filter(n => n.id !== nodeId));
  };

  const openTerminal = useCallback((node: EdgeNode) => {
    if (!workspaceId) return;
    const wsNum = parseInt(workspaceId, 10);
    if (isNaN(wsNum)) return;
    const userId = parseInt(node.identity.userAccountId, 10);
    if (isNaN(userId)) return;

    setActiveNodeId(node.id);
    shell.connect(wsNum, userId, node.identity.name || node.identity.email, (info) => {
      setNodes(prev => prev.map(n => n.id === node.id ? { ...n, sysInfo: info } : n));
    });
  }, [workspaceId, shell.connect]);

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') { shell.submit(); return; }
    if (e.key === 'Escape') { closeTerminal(); return; }
    if (e.ctrlKey && e.key === 'c') { e.preventDefault(); shell.interrupt(); return; }
    if (e.ctrlKey && e.key === 'l') { e.preventDefault(); shell.clear(); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); shell.navigateHistory('up'); return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); shell.navigateHistory('down'); return; }
  }

  const activeNode = activeNodeId ? nodes.find(n => n.id === activeNodeId) ?? null : null;

  // ── Terminal view ────────────────────────────────────────────────────────────
  if (activeNodeId) {
    return (
      <div className="shell-panel">
        <div className="shell-header">
          <div className="edge-terminal-nav">
            <button className="edge-back-btn" onClick={closeTerminal} title="Back to nodes">←</button>
            <span className="shell-header-label">
              {activeNode ? (activeNode.identity.name || activeNode.identity.email).toUpperCase() : 'TERMINAL'}
            </span>
          </div>
          <div className="shell-header-actions">
            <button className="shell-ctrl-btn" onClick={shell.clear} title="Clear (Ctrl+L)">CLR</button>
            <button className="shell-ctrl-btn" onClick={shell.interrupt} title="Interrupt (Ctrl+C)" disabled={!shell.running}>^C</button>
            <span className={`shell-header-status ${shell.running ? 'shell-status-live' : 'shell-status-off'}`}>
              {shell.running ? 'LIVE' : 'OFFLINE'}
            </span>
          </div>
        </div>

        <div className="shell-body" onClick={() => { if (!shell.elapsed) inputRef.current?.focus(); }}>
          {shell.lines.map(l => (
            <div key={l.id} className={`shell-line shell-line-${l.kind}`}>
              {l.kind === 'sent' && <span className="shell-prompt-glyph">{'> '}</span>}
              {l.text}
            </div>
          ))}
          {shell.elapsed !== null ? (
            <div className="shell-input-line shell-waiting-line">
              <span className="shell-prompt-glyph">{'> '}</span>
              <span className="shell-elapsed-inline">{(shell.elapsed / 1000).toFixed(1)}s</span>
            </div>
          ) : (
            <div className="shell-input-line">
              <span className="shell-prompt-glyph">{'> '}</span>
              <input
                ref={inputRef}
                className="shell-input"
                value={shell.input}
                onChange={e => shell.setInput(e.target.value)}
                onKeyDown={onKeyDown}
                disabled={!shell.running}
                placeholder={!shell.running ? 'connecting…' : ''}
                spellCheck={false}
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="off"
                autoFocus
              />
            </div>
          )}
          <div ref={bottomRef} />
        </div>
      </div>
    );
  }

  // ── Node list view ───────────────────────────────────────────────────────────
  return (
    <div className="edge-nodes-panel">
      <div className="edge-nodes-header">
        <span className="edge-nodes-title">EDGE NODES</span>
        <button className="edge-nodes-add-btn" onClick={openPicker} title="Connect identity">+</button>
      </div>

      {pickerOpen && (
        <div className="identity-picker">
          <div className="identity-picker-header">
            <span className="identity-picker-title">SELECT IDENTITY</span>
            <button className="identity-picker-close" onClick={() => setPickerOpen(false)}>✕</button>
          </div>
          {loadingIds
            ? <div className="identity-picker-msg">LOADING...</div>
            : identities?.length === 0
              ? <div className="identity-picker-msg">No identities found</div>
              : (
                <div className="identity-picker-list">
                  {identities?.map(ident => (
                    <button key={ident.identityId} className="identity-picker-item" onClick={() => addNode(ident)}>
                      <span className="identity-item-name">{ident.name || ident.email || ident.userAccountId}</span>
                      {ident.email && <span className="identity-item-sub">{ident.email}</span>}
                    </button>
                  ))}
                </div>
              )
          }
        </div>
      )}

      {!pickerOpen && nodes.length === 0 && (
        <div className="edge-nodes-empty">No nodes — press + to connect</div>
      )}

      <div className="edge-node-list">
        {nodes.map(node => (
          <div key={node.id} className="edge-node-card">
            <div className="edge-node-card-head" onClick={() => onNodeFocus(MOCK_LNG, MOCK_LAT)} style={{ cursor: 'pointer' }}>
              <span className="edge-node-dot">▸</span>
              <span className="edge-node-name">{node.identity.name || node.identity.email}</span>
              <button className="edge-node-remove" onClick={e => { e.stopPropagation(); removeNode(node.id); }} title="Remove">✕</button>
            </div>

            <div className="edge-node-meta">
              <div className="edge-node-row">
                <span className="edge-node-key">UID</span>
                <span className="edge-node-val">{node.identity.userAccountId}</span>
              </div>
              {node.sysInfo && (
                <>
                  <div className="edge-node-row">
                    <span className="edge-node-key">HOST</span>
                    <span className="edge-node-val">{node.sysInfo.hostname}</span>
                  </div>
                  <div className="edge-node-row">
                    <span className="edge-node-key">CPU</span>
                    <span className="edge-node-val">{node.sysInfo.cpu}</span>
                  </div>
                  <div className="edge-node-row">
                    <span className="edge-node-key">OS</span>
                    <span className="edge-node-val">{node.sysInfo.os}</span>
                  </div>
                </>
              )}
            </div>

            <button
              className="edge-terminal-btn"
              onClick={() => openTerminal(node)}
              disabled={!workspaceId}
            >
              ⌨ TERMINAL
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
