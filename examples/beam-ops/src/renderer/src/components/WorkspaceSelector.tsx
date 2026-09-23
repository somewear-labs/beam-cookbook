import { useState, useEffect } from 'react';
import { beamController } from '../controllers/BeamController';
import { beamApi, WorkspaceInfo } from '../services/beamApi';

interface WorkspaceSelectorProps {
  onWorkspaceActivated: () => void;
}

type View = 'select' | 'provision';

type ProvisionStep = 'input' | 'connecting' | 'provisioning' | 'done' | 'error';

function ProvisionView({ onBack, onDone }: { onBack: () => void; onDone: () => void }) {
  const [step, setStep] = useState<ProvisionStep>('input');
  const [deviceName, setDeviceName] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const handleProvision = async () => {
    if (!deviceName.trim() || !apiKey.trim()) return;
    setErrorMsg(null);
    setStep('connecting');

    const connected = await beamApi.deviceConnect();
    if (!connected) {
      setStep('error');
      setErrorMsg('No Beam device detected via USB. Plug one in and try again.');
      return;
    }

    setStep('provisioning');
    try {
      const keyResult = await beamApi.setApiKey(apiKey.trim());
      if (!keyResult.success) {
        setStep('error');
        setErrorMsg(keyResult.message || 'API key authentication failed.');
        return;
      }
      await beamApi.provisionEdge(deviceName.trim());
      setStep('done');
    } catch (err) {
      setStep('error');
      setErrorMsg(err instanceof Error ? err.message : String(err));
    }
  };

  const handleReset = () => {
    setStep('input');
    setErrorMsg(null);
  };

  const canSubmit = deviceName.trim().length > 0 && apiKey.trim().length > 0;

  return (
    <>
      <div className="workspace-modal-header">
        <span className="workspace-hex-icon">⬡</span>
        <span className="workspace-modal-title">PROVISION EDGE DEVICE</span>
      </div>
      <div className="workspace-modal-divider" />

      <div className="workspace-modal-body">
        {step === 'input' && (
          <>
            <div className="provision-instructions">
              Plug your Beam device in via USB, then enter a name for this edge node and your Somewear API key.
            </div>
            <div className="provision-field">
              <label className="provision-label">DEVICE NAME</label>
              <input
                className="provision-input"
                type="text"
                placeholder="e.g. EDGE-NODE-01"
                value={deviceName}
                onChange={e => setDeviceName(e.target.value.toUpperCase())}
                autoFocus
                maxLength={32}
              />
            </div>
            <div className="provision-field">
              <label className="provision-label">API KEY</label>
              <input
                className="provision-input"
                type="password"
                placeholder="Somewear API key"
                value={apiKey}
                onChange={e => setApiKey(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && canSubmit && handleProvision()}
              />
            </div>
          </>
        )}

        {step === 'connecting' && (
          <div className="provision-status">
            <span className="blink">▋</span> CONNECTING TO BEAM DEVICE...
          </div>
        )}

        {step === 'provisioning' && (
          <div className="provision-status">
            <span className="blink">▋</span> PROVISIONING {deviceName}...
          </div>
        )}

        {step === 'done' && (
          <div className="provision-status provision-status--ok">
            ✓ DEVICE PROVISIONED SUCCESSFULLY
          </div>
        )}

        {step === 'error' && (
          <div className="provision-status provision-status--err">
            {errorMsg ?? 'Provisioning failed.'}
          </div>
        )}
      </div>

      <div className="workspace-modal-divider" />
      <div className="workspace-modal-footer">
        {step === 'done' ? (
          <button className="workspace-activate-btn" onClick={onDone}>
            CONTINUE
          </button>
        ) : step === 'error' ? (
          <div className="provision-footer-row">
            <button className="workspace-back-btn" onClick={onBack}>BACK</button>
            <button className="workspace-activate-btn" onClick={handleReset}>RETRY</button>
          </div>
        ) : (
          <div className="provision-footer-row">
            <button
              className="workspace-back-btn"
              onClick={onBack}
              disabled={step !== 'input'}
            >
              BACK
            </button>
            <button
              className="workspace-activate-btn"
              onClick={handleProvision}
              disabled={!canSubmit || step !== 'input'}
            >
              PROVISION
            </button>
          </div>
        )}
      </div>
    </>
  );
}

export default function WorkspaceSelector({ onWorkspaceActivated }: WorkspaceSelectorProps) {
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [activating, setActivating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>('select');

  useEffect(() => {
    let mounted = true;
    beamController
      .listWorkspaces()
      .then((res) => {
        if (!mounted) return;
        setWorkspaces(res.workspaces);
        if (res.workspaces.length > 0) {
          setSelectedId(res.workspaces[0].workspaceId);
        } else {
          setView('provision');
        }
        setLoading(false);
      })
      .catch((err) => {
        if (!mounted) return;
        setError(`Failed to load workspaces: ${err instanceof Error ? err.message : String(err)}`);
        setLoading(false);
      });
    return () => { mounted = false; };
  }, []);

  const handleActivate = async () => {
    if (!selectedId) return;
    setActivating(true);
    setError(null);
    try {
      await beamController.activateWorkspace(selectedId);
      onWorkspaceActivated();
    } catch (err) {
      setError(`Activation failed: ${err instanceof Error ? err.message : String(err)}`);
      setActivating(false);
    }
  };

  const handleProvisionDone = () => {
    beamController.initialize().then(() => onWorkspaceActivated()).catch(() => onWorkspaceActivated());
  };

  return (
    <div className="workspace-overlay">
      <div className="workspace-modal">
        {view === 'provision' ? (
          <ProvisionView
            onBack={() => setView('select')}
            onDone={handleProvisionDone}
          />
        ) : (
          <>
            <div className="workspace-modal-header">
              <span className="workspace-hex-icon">⬡</span>
              <span className="workspace-modal-title">SELECT WORKSPACE</span>
            </div>
            <div className="workspace-modal-divider" />

            <div className="workspace-modal-body">
              {loading && (
                <div className="workspace-loading">
                  <span className="blink">▋</span> LOADING WORKSPACES...
                </div>
              )}
              {!loading && error && <div className="workspace-error">{error}</div>}
              {!loading && !error && workspaces.length === 0 && (
                <div className="workspace-empty">NO WORKSPACES AVAILABLE</div>
              )}
              {!loading && workspaces.map((ws) => (
                <div
                  key={ws.workspaceId}
                  className={`workspace-item ${selectedId === ws.workspaceId ? 'selected' : ''}`}
                  onClick={() => setSelectedId(ws.workspaceId)}
                >
                  <span className="workspace-radio">
                    {selectedId === ws.workspaceId ? '●' : '○'}
                  </span>
                  <span className="workspace-name">{ws.name}</span>
                  <span className="workspace-id">[{ws.workspaceId}]</span>
                </div>
              ))}
            </div>

            <div className="workspace-modal-divider" />
            <div className="workspace-modal-footer">
              {error && !loading && <div className="workspace-error-inline">{error}</div>}
              <button
                className="workspace-activate-btn"
                onClick={handleActivate}
                disabled={!selectedId || activating || loading}
              >
                {activating ? <><span className="blink">▋</span> ACTIVATING...</> : 'ACTIVATE WORKSPACE'}
              </button>
              <button
                className="workspace-provision-btn"
                onClick={() => setView('provision')}
                disabled={loading}
              >
                + PROVISION EDGE DEVICE
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
