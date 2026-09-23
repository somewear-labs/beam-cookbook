import { useEffect, useState, useCallback, useRef } from 'react';
import type maplibregl from 'maplibre-gl';
import MapView from './components/MapView';
import WorkspaceSelector from './components/WorkspaceSelector';
import BeamStatus from './components/BeamStatus';
import PayloadFeed from './components/PayloadFeed';
import EdgeNodesPanel from './components/EdgeNodesPanel';
import EdgeComputePanel from './components/EdgeComputePanel';
import Sidebar from './components/Sidebar';
import SeismographPanel from './components/SeismographPanel';
import SysmonPanel from './components/SysmonPanel';
import GeoSearch from './components/GeoSearch';
import DeviceQueueReportBox from './components/DeviceQueueReportBox';
import { beamController, ConnectionState } from './controllers/BeamController';
import { mapController } from './controllers/MapController';
import { AuthStateResponse, WorkspaceInfo, beamApi } from './services/beamApi';
import './styles/index.css';
import './styles/tactical.css';

type AppPhase =
  | 'initializing'
  | 'daemon-offline'
  | 'not-authenticated'
  | 'select-workspace'
  | 'operational';

interface CoordState {
  lng: number;
  lat: number;
  zoom: number;
}

interface LayerEntry {
  filePath: string;
  name: string;
  center: [number, number] | null;
  zoom: number | null;
}

function toLayerEntry(p: { filePath: string; name: string; centerLng: number | null; centerLat: number | null; zoom: number | null }): LayerEntry {
  return {
    filePath: p.filePath,
    name: p.name,
    center: p.centerLng !== null && p.centerLat !== null ? [p.centerLng, p.centerLat] : null,
    zoom: p.zoom
  };
}

function ApiKeyForm({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [apiKey, setApiKey] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => { inputRef.current?.focus(); }, []);

  const handleSubmit = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();
    const key = apiKey.trim();
    if (!key) return;
    setSubmitting(true);
    setError(null);
    try {
      const result = await beamApi.setApiKey(key);
      if (result.success && result.isAuthenticated) {
        onAuthenticated();
      } else if (result.success && !result.isAuthenticated) {
        setError('API key saved but sign-in failed. Restart the Beam daemon and try again.');
      } else {
        setError(result.message || 'Authentication failed. Check your API key and try again.');
      }
    } catch {
      setError('Could not reach the Beam daemon. Ensure it is running and try again.');
    } finally {
      setSubmitting(false);
    }
  }, [apiKey, onAuthenticated]);

  return (
    <div className="overlay-center">
      <div className="splash-card">
        <div className="splash-logo">⬡ BEAM OPS</div>
        <div className="apikey-title">ENTER API KEY</div>
        <div className="apikey-hint">Paste your Somewear API key to authenticate.</div>
        <form className="apikey-form" onSubmit={handleSubmit}>
          <input
            ref={inputRef}
            className="apikey-input"
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="API KEY"
            autoComplete="off"
            disabled={submitting}
          />
          {error && <div className="apikey-error">{error}</div>}
          <button
            className="retry-btn"
            type="submit"
            disabled={submitting || !apiKey.trim()}
          >
            {submitting ? 'AUTHENTICATING...' : 'AUTHENTICATE'}
          </button>
        </form>
      </div>
    </div>
  );
}

export default function App() {
  const [phase, setPhase] = useState<AppPhase>('initializing');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [initStatus, setInitStatus] = useState('INITIALIZING...');
  const [connectionState, setConnectionState] = useState<ConnectionState>('disconnected');
  const [authState, setAuthState] = useState<AuthStateResponse | null>(null);
  const [activeWorkspace, setActiveWorkspace] = useState<WorkspaceInfo | null>(null);
  const [coords, setCoords] = useState<CoordState>({ lng: -121.5241, lat: 45.7054, zoom: 12 });
  const [tileVersion, setTileVersion] = useState(0);
  const [flyTarget, setFlyTarget] = useState<{ center: [number, number]; zoom: number } | null>(null);
  const [currentLayerName, setCurrentLayerName] = useState('hood-river');
  const [basemapId, setBasemapId] = useState('dark');
  const [mapboxToken, setMapboxToken] = useState<string | null | undefined>(undefined);

  const [map, setMap] = useState<maplibregl.Map | null>(null);

  useEffect(() => {
    if (!map) return;
    mapController.attach(map);
    return () => mapController.detach();
  }, [map]);

  const [savedLayers, setSavedLayers] = useState<LayerEntry[]>([]);
  const [edgeComputeOpen, setEdgeComputeOpen] = useState(false);

  useEffect(() => {
    window.layersApi.list().then(rows => setSavedLayers(rows.map(toLayerEntry)));
  }, []);

  useEffect(() => {
    window.configApi.getMapboxToken().then(t => setMapboxToken(t ?? null));
  }, []);

  const determinePhase = useCallback(
    (
      connState: ConnectionState,
      auth: AuthStateResponse | null,
      ws: WorkspaceInfo | null
    ): AppPhase => {
      if (connState === 'connecting') return 'initializing';
      if (connState === 'error') {
        // Distinguish offline vs auth error
        if (!auth) return 'daemon-offline';
        if (auth.state !== 'SignedIn') return 'not-authenticated';
        return 'daemon-offline';
      }
      if (connState === 'connected') {
        if (!ws) return 'select-workspace';
        return 'operational';
      }
      return 'initializing';
    },
    []
  );

  useEffect(() => {
    // Subscribe to controller events
    const onConnChange = (state: unknown) => {
      const s = state as ConnectionState;
      setConnectionState(s);
      setPhase(determinePhase(s, beamController.authState, beamController.activeWorkspace));
    };

    const onAuthChange = (auth: unknown) => {
      setAuthState(auth as AuthStateResponse | null);
    };

    const onWsChange = (ws: unknown) => {
      const workspace = ws as WorkspaceInfo | null;
      setActiveWorkspace(workspace);
      setPhase(
        determinePhase(beamController.connectionState, beamController.authState, workspace)
      );
    };

    const onError = (err: unknown) => {
      const e = err instanceof Error ? err.message : String(err);
      setErrorMsg(e);

      const auth = beamController.authState;
      if (!auth) {
        setPhase('daemon-offline');
      } else if (auth.state !== 'SignedIn') {
        setPhase('not-authenticated');
      } else {
        setPhase('daemon-offline');
      }
    };

    const onInitStatus = (msg: unknown) => setInitStatus(msg as string);

    beamController.on('connectionStateChange', onConnChange);
    beamController.on('authStateChange', onAuthChange);
    beamController.on('workspaceChange', onWsChange);
    beamController.on('initStatus', onInitStatus);
    beamController.on('error', onError);

    // Start initialization
    beamController.initialize();

    return () => {
      beamController.off('connectionStateChange', onConnChange);
      beamController.off('authStateChange', onAuthChange);
      beamController.off('workspaceChange', onWsChange);
      beamController.off('initStatus', onInitStatus);
      beamController.off('error', onError);
    };
  }, [determinePhase]);

  const handleWorkspaceActivated = () => {
    setActiveWorkspace(beamController.activeWorkspace);
    setPhase('operational');
  };

  const handleCoordinatesChange = (lng: number, lat: number, zoom: number) => {
    setCoords({ lng, lat, zoom });
  };

  const handleLayerLoaded = (meta: { filePath: string; center: [number, number] | null; zoom: number | null; name: string }) => {
    setCurrentLayerName(meta.name);
    setTileVersion((v) => v + 1);
    if (meta.center) {
      setFlyTarget({ center: meta.center, zoom: meta.zoom ?? 12 });
    }
    const persisted = {
      filePath: meta.filePath,
      name: meta.name,
      centerLng: meta.center ? meta.center[0] : null,
      centerLat: meta.center ? meta.center[1] : null,
      zoom: meta.zoom
    };
    window.layersApi.upsert(persisted);
    const entry: LayerEntry = { filePath: meta.filePath, name: meta.name, center: meta.center, zoom: meta.zoom };
    setSavedLayers(prev => [entry, ...prev.filter(l => l.filePath !== meta.filePath)]);
  };

  const handleLayerRemove = (filePath: string) => {
    window.layersApi.remove(filePath);
    setSavedLayers(prev => prev.filter(l => l.filePath !== filePath));
  };

  const authLabel = authState
    ? authState.state === 'SignedIn'
      ? 'SIGNED_IN'
      : authState.state === 'SignedOut'
        ? 'SIGNED_OUT'
        : 'UNKNOWN'
    : 'CHECKING...';

  const authCls =
    authState?.state === 'SignedIn'
      ? 'auth-signed-in'
      : authState?.state === 'SignedOut'
        ? 'auth-signed-out'
        : 'auth-unknown';

  const connCls =
    connectionState === 'connected'
      ? 'conn-connected'
      : connectionState === 'connecting'
        ? 'conn-connecting'
        : 'conn-error';

  return (
    <div className="app-root">
      {/* Top bar — always rendered */}
      <header className="top-bar">
        <div className="top-bar-left">
          <span className="app-logo">
            <span className="logo-hex">⬡</span> BEAM OPS
          </span>
        </div>

        <div className="top-bar-center">
          <span className={`top-bar-badge ${authCls}`}>
            AUTH: {authLabel}
          </span>
        </div>

        <div className="top-bar-right">
          <span className={`top-bar-badge ${connCls}`}>
            {connectionState === 'connected'
              ? 'BEAM: ONLINE'
              : connectionState === 'connecting'
                ? 'BEAM: CONNECTING'
                : 'BEAM: OFFLINE'}
          </span>
          {activeWorkspace && (
            <span className="top-bar-badge ws-badge">
              WS: {activeWorkspace.name}
            </span>
          )}
        </div>
      </header>

      {/* Icon sidebar — always rendered */}
      <Sidebar
        onLayerLoaded={handleLayerLoaded}
        savedLayers={savedLayers}
        onLayerRemove={handleLayerRemove}
        basemapId={basemapId}
        mapboxToken={mapboxToken ?? null}
        onBasemapChange={setBasemapId}
        edgeComputeOpen={edgeComputeOpen}
        onEdgeComputeToggle={() => setEdgeComputeOpen(p => !p)}
      />

      {/* Map container — deferred until mapboxToken state is resolved so initial style is correct */}
      <div className="map-container">
        {mapboxToken !== undefined && (
          <MapView
            onCoordinatesChange={handleCoordinatesChange}
            tileVersion={tileVersion}
            flyTarget={flyTarget}
            basemapId={basemapId}
            mapboxToken={mapboxToken}
            onMapReady={setMap}
          />
        )}
      </div>

      {/* Overlays */}

      {/* Initializing splash */}
      {phase === 'initializing' && (
        <div className="overlay-center">
          <div className="splash-card">
            <div className="splash-logo">⬡ BEAM OPS</div>
            <div className="splash-status">
              <span className="blink">▋</span> {initStatus}
            </div>
          </div>
        </div>
      )}

      {/* Daemon offline */}
      {phase === 'daemon-offline' && (
        <div className="overlay-center">
          <div className="splash-card error">
            <div className="splash-logo">⬡ BEAM OPS</div>
            <div className="error-title">DAEMON OFFLINE</div>
            <div className="error-msg">
              {errorMsg ?? 'Unable to reach Beam daemon at localhost:9091'}
            </div>
            <div className="error-hint">
              Ensure the Beam daemon is running, then restart the application.
            </div>
            <button
              className="retry-btn"
              onClick={() => {
                setPhase('initializing');
                setErrorMsg(null);
                beamController.initialize();
              }}
            >
              RETRY CONNECTION
            </button>
          </div>
        </div>
      )}

      {/* Not authenticated */}
      {phase === 'not-authenticated' && (
        <ApiKeyForm onAuthenticated={() => {
          setPhase('initializing');
          setErrorMsg(null);
          beamController.initialize();
        }} />
      )}

      {/* Workspace selector modal */}
      {phase === 'select-workspace' && (
        <WorkspaceSelector onWorkspaceActivated={handleWorkspaceActivated} />
      )}

      {/* Operational overlays */}
      {phase === 'operational' && (
        <>
          {/* Left panel: beam status or edge compute (top), always edge nodes below */}
          <div className="overlay-left">
            <GeoSearch
              token={mapboxToken ?? null}
              onFlyTo={(lng, lat, zoom) => setFlyTarget({ center: [lng, lat], zoom })}
            />
            {edgeComputeOpen ? <EdgeComputePanel /> : <BeamStatus />}
            <EdgeNodesPanel
              workspaceId={activeWorkspace?.workspaceId ?? null}
              map={map}
              onNodeFocus={(lng, lat) => setFlyTarget({ center: [lng, lat], zoom: 14 })}
            />
          </div>

          {/* Bottom-right: coordinate display */}
          <div className="overlay-bottom-right">
            <div className="coord-panel">
              <div className="coord-row">
                <span className="coord-label">LAT</span>
                <span className="coord-value">{coords.lat.toFixed(4)}° N</span>
              </div>
              <div className="coord-row">
                <span className="coord-label">LON</span>
                <span className="coord-value">{Math.abs(coords.lng).toFixed(4)}° W</span>
              </div>
              <div className="coord-row">
                <span className="coord-label">Z</span>
                <span className="coord-value">{coords.zoom.toFixed(1)}</span>
              </div>
            </div>
          </div>

          {/* Right side: device queue report + payload feed */}
          <div className="overlay-right">
            <DeviceQueueReportBox />
            <PayloadFeed workspaceId={activeWorkspace?.workspaceId ?? null} />
          </div>

          {/* Bottom center: seismograph + sysmon visualizations */}
          <div className="overlay-bottom-center">
            <SeismographPanel />
            <SysmonPanel />
          </div>
        </>
      )}
    </div>
  );
}
