import { useEffect, useRef, useState } from 'react';
import { BASEMAP_OPTIONS } from './BasemapSelector';
import NodesPanel from './NodesPanel';

interface LayerEntry {
  filePath: string;
  name: string;
  center: [number, number] | null;
  zoom: number | null;
}

interface SidebarProps {
  onLayerLoaded: (meta: { filePath: string; center: [number, number] | null; zoom: number | null; name: string }) => void;
  savedLayers: LayerEntry[];
  onLayerRemove: (filePath: string) => void;
  basemapId: string;
  mapboxToken: string | null;
  onBasemapChange: (id: string) => void;
  edgeComputeOpen: boolean;
  onEdgeComputeToggle: () => void;
}

function IconMap() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="3 6 9 3 15 6 21 3 21 18 15 21 9 18 3 21"/>
      <line x1="9" y1="3" x2="9" y2="18"/>
      <line x1="15" y1="6" x2="15" y2="21"/>
    </svg>
  );
}

function IconServer() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
      <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
      <line x1="6" y1="6" x2="6.01" y2="6"/>
      <line x1="6" y1="18" x2="6.01" y2="18"/>
    </svg>
  );
}

function IconMesh() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="4" r="2"/>
      <circle cx="4" cy="20" r="2"/>
      <circle cx="20" cy="20" r="2"/>
      <line x1="12" y1="6" x2="4" y2="18"/>
      <line x1="12" y1="6" x2="20" y2="18"/>
      <line x1="6" y1="20" x2="18" y2="20"/>
    </svg>
  );
}

function IconLayersUpload() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2L2 7l10 5 10-5-10-5z"/>
      <path d="M2 17l10 5 10-5"/>
      <path d="M2 12l10 5 10-5"/>
      <polyline points="16 16 19 13 22 16"/>
      <line x1="19" y1="13" x2="19" y2="21"/>
    </svg>
  );
}

function IconLayersLoading() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="sidebar-spin">
      <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/>
    </svg>
  );
}


export default function Sidebar({ onLayerLoaded, savedLayers, onLayerRemove, basemapId, mapboxToken, onBasemapChange, edgeComputeOpen, onEdgeComputeToggle }: SidebarProps) {
  const [panelOpen, setPanelOpen] = useState(false);
  const [meshOpen, setMeshOpen] = useState(false);
  const [loadingLayer, setLoadingLayer] = useState(false);
  const [layerSuccess, setLayerSuccess] = useState(false);
  const [loadingFilePath, setLoadingFilePath] = useState<string | null>(null);
  const [errorFilePath, setErrorFilePath] = useState<string | null>(null);
  const sidebarRef = useRef<HTMLElement>(null);

  useEffect(() => {
    if (!panelOpen && !meshOpen) return;
    const handleClick = (e: MouseEvent) => {
      if (sidebarRef.current && !sidebarRef.current.contains(e.target as Node)) {
        setPanelOpen(false);
        setMeshOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [panelOpen, meshOpen]);

  const loadLayer = async (filePath: string) => {
    setLoadingFilePath(filePath);
    setErrorFilePath(null);
    try {
      const meta = await window.tileApi.load(filePath);
      onLayerLoaded({ filePath, ...meta });
      setLayerSuccess(true);
      setTimeout(() => setLayerSuccess(false), 2000);
      setPanelOpen(false);
    } catch {
      setErrorFilePath(filePath);
      setTimeout(() => setErrorFilePath(null), 3000);
    } finally {
      setLoadingFilePath(null);
    }
  };

  const handleBrowse = async () => {
    if (loadingLayer) return;
    setLoadingLayer(true);
    setLayerSuccess(false);
    try {
      const filePath = await window.tileApi.openDialog();
      if (!filePath) return;
      const meta = await window.tileApi.load(filePath);
      onLayerLoaded({ filePath, ...meta });
      setLayerSuccess(true);
      setTimeout(() => setLayerSuccess(false), 2000);
      setPanelOpen(false);
    } catch (err) {
      console.error('[Sidebar] Failed to load layer:', err);
    } finally {
      setLoadingLayer(false);
    }
  };

  const layerIconCls = [
    'sidebar-icon',
    panelOpen ? 'sidebar-icon--active' : '',
    layerSuccess ? 'sidebar-icon--success' : '',
    loadingLayer ? 'sidebar-icon--loading' : ''
  ].filter(Boolean).join(' ');

  return (
    <nav className="sidebar" ref={sidebarRef}>
      <div className="sidebar-section">
        <button className="sidebar-icon sidebar-icon--active" data-tooltip="Map View" tabIndex={-1}>
          <IconMap />
        </button>

        <button
          className={layerIconCls}
          data-tooltip={loadingLayer ? 'Loading...' : layerSuccess ? 'Loaded!' : 'Layers & Basemap'}
          onClick={() => { if (!loadingLayer) { setPanelOpen(p => !p); setMeshOpen(false); } }}
          disabled={loadingLayer}
        >
          {loadingLayer ? <IconLayersLoading /> : <IconLayersUpload />}
        </button>

        <button
          className={`sidebar-icon${meshOpen ? ' sidebar-icon--active' : ''}`}
          data-tooltip="Mesh Nodes"
          onClick={() => { setMeshOpen(p => !p); setPanelOpen(false); }}
        >
          <IconMesh />
        </button>

        <button
          className={`sidebar-icon${edgeComputeOpen ? ' sidebar-icon--active' : ''}`}
          data-tooltip="Edge Compute"
          onClick={onEdgeComputeToggle}
        >
          <IconServer />
        </button>
      </div>

      {meshOpen && (
        <div className="mesh-panel">
          <NodesPanel />
        </div>
      )}

      {panelOpen && (
        <div className="layer-panel">
          <div className="layer-panel-header">TILE LAYERS</div>
          <button className="layer-panel-browse" onClick={handleBrowse}>
            + Browse for file…
          </button>
          {savedLayers.length > 0 && (
            <>
              <div className="layer-panel-divider" />
              <div className="layer-panel-list">
                {savedLayers.map(layer => (
                  <div
                    key={layer.filePath}
                    className={[
                      'layer-panel-item',
                      loadingFilePath === layer.filePath ? 'layer-panel-item--loading' : '',
                      errorFilePath === layer.filePath ? 'layer-panel-item--error' : ''
                    ].filter(Boolean).join(' ')}
                  >
                    <button
                      className="layer-panel-item-name"
                      onClick={() => loadingFilePath ? undefined : loadLayer(layer.filePath)}
                      disabled={!!loadingFilePath}
                      title={layer.filePath}
                    >
                      {errorFilePath === layer.filePath ? '⚠ ' : ''}{layer.name}
                    </button>
                    <button
                      className="layer-panel-item-remove"
                      onClick={() => onLayerRemove(layer.filePath)}
                      title="Remove from list"
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            </>
          )}

          <div className="layer-panel-divider" />
          <div className="layer-panel-header">BASEMAP</div>
          <div className="layer-panel-list">
            {BASEMAP_OPTIONS.map(opt => {
              const disabled = !!opt.mapboxStyle && !mapboxToken;
              return (
                <div key={opt.id} className="layer-panel-item">
                  <button
                    className={`layer-panel-item-name${basemapId === opt.id ? ' layer-panel-item-name--active' : ''}`}
                    onClick={() => { if (!disabled) onBasemapChange(opt.id); }}
                    disabled={disabled}
                    title={disabled ? 'No Mapbox token configured' : undefined}
                  >
                    {opt.label}
                  </button>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </nav>
  );
}
