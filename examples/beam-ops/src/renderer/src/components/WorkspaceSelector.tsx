import { useState, useEffect, useRef } from 'react';
import { beamController } from '../controllers/BeamController';
import { beamApi, WorkspaceInfo, OrgItem } from '../services/beamApi';

interface WorkspaceSelectorProps {
  onWorkspaceActivated: () => void;
}

type View = 'select' | 'provision';

const DEFAULT_SERVER = 'api.somewear.co';

type ProvisionStep =
  | 'name'
  | 'connecting'
  | 'auth'
  | 'auth-validating'
  | 'auth-browser'
  | 'auth-org'
  | 'provisioning'
  | 'done'
  | 'error';

function ProvisionView({ onBack, onDone }: { onBack: () => void; onDone: () => void }) {
  const [step, setStep] = useState<ProvisionStep>('name');
  const [deviceName, setDeviceName] = useState('');
  const [serverHost, setServerHost] = useState('');
  const [nonce, setNonce] = useState('');
  const [appUrl, setAppUrl] = useState('');
  const [orgs, setOrgs] = useState<OrgItem[]>([]);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const stopPolling = () => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  };

  useEffect(() => stopPolling, []);

  const handleConnect = async () => {
    if (!deviceName.trim()) return;
    setErrorMsg(null);
    setStep('connecting');
    const connected = await beamApi.deviceConnect();
    if (!connected) {
      setStep('error');
      setErrorMsg('No Beam device detected via USB. Plug one in and try again.');
      return;
    }
    setStep('auth');
  };

  const handleAuthenticate = async () => {
    const host = serverHost.trim() || DEFAULT_SERVER;
    setErrorMsg(null);
    setStep('auth-validating');
    try {
      const result = await beamApi.validateServer(host);
      if (!result.valid || !result.appUrl || !result.nonce) {
        setStep('auth');
        setErrorMsg(result.error ?? 'Server validation failed. Check the domain and try again.');
        return;
      }
      const callbackUrl = `${result.appUrl}?callback_port=9091&nonce=${result.nonce}`;
      await beamApi.openExternal(callbackUrl);
      setNonce(result.nonce);
      setAppUrl(result.appUrl);
      setStep('auth-browser');

      const deadline = Date.now() + 5 * 60 * 1000;
      pollRef.current = setInterval(async () => {
        if (Date.now() > deadline) {
          stopPolling();
          setStep('error');
          setErrorMsg('Authentication timed out. Try again.');
          return;
        }
        try {
          const tokenResult = await beamApi.checkAuthToken(result.nonce!);
          if (tokenResult?.token) {
            stopPolling();
            await handleOrgFetch(result.nonce!);
          }
        } catch { /* keep polling */ }
      }, 2000);
    } catch (err) {
      setStep('auth');
      setErrorMsg(err instanceof Error ? err.message : String(err));
    }
  };

  const handleOrgFetch = async (activeNonce: string) => {
    try {
      const { organizations } = await beamApi.fetchOrganizations(activeNonce);
      if (!organizations || organizations.length === 0) {
        setStep('error');
        setErrorMsg('No organizations found. Contact your org admin to be added.');
        return;
      }
      if (organizations.length === 1) {
        await handleCreateKey(organizations[0].id, activeNonce);
      } else {
        setOrgs(organizations);
        setStep('auth-org');
      }
    } catch (err) {
      setStep('error');
      setErrorMsg(err instanceof Error ? err.message : String(err));
    }
  };

  const handleCreateKey = async (organizationId: string, activeNonce: string) => {
    setStep('provisioning');
    try {
      const keyResult = await beamApi.createApiKey(organizationId, activeNonce);
      if (!keyResult.success) {
        setStep('error');
        setErrorMsg(keyResult.message || 'API key creation failed.');
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
    stopPolling();
    setStep('name');
    setErrorMsg(null);
    setNonce('');
    setAppUrl('');
    setOrgs([]);
  };

  return (
    <>
      <div className="workspace-modal-header">
        <span className="workspace-hex-icon">⬡</span>
        <span className="workspace-modal-title">PROVISION EDGE DEVICE</span>
      </div>
      <div className="workspace-modal-divider" />

      <div className="workspace-modal-body">
        {step === 'name' && (
          <>
            <div className="provision-instructions">
              You are provisioning this machine as a Somewear edge compute device. Give it a name to continue.
            </div>
            <div className="provision-field">
              <label className="provision-label">DEVICE NAME</label>
              <input
                className="provision-input"
                type="text"
                placeholder="e.g. EDGE-NODE-01"
                value={deviceName}
                onChange={e => setDeviceName(e.target.value.toUpperCase())}
                onKeyDown={e => e.key === 'Enter' && deviceName.trim() && handleConnect()}
                autoFocus
                maxLength={32}
              />
            </div>
          </>
        )}

        {step === 'connecting' && (
          <div className="provision-status">
            <span className="blink">▋</span> CONNECTING TO BEAM DEVICE...
          </div>
        )}

        {step === 'auth' && (
          <>
            <div className="provision-instructions">
              Enter your Somewear domain to open the browser and sign in.
            </div>
            <div className="provision-field">
              <label className="provision-label">SOMEWEAR DOMAIN</label>
              <input
                className="provision-input"
                type="text"
                placeholder={DEFAULT_SERVER}
                value={serverHost}
                onChange={e => setServerHost(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleAuthenticate()}
                autoFocus
              />
            </div>
            {errorMsg && (
              <div className="provision-status provision-status--err">{errorMsg}</div>
            )}
          </>
        )}

        {step === 'auth-validating' && (
          <div className="provision-status">
            <span className="blink">▋</span> VALIDATING SERVER...
          </div>
        )}

        {step === 'auth-browser' && (
          <div className="provision-status">
            <span className="blink">▋</span> WAITING FOR BROWSER AUTHENTICATION...
          </div>
        )}

        {step === 'auth-org' && (
          <>
            <div className="provision-instructions">Select your organization.</div>
            {orgs.map(org => (
              <div
                key={org.id}
                className="workspace-item"
                onClick={() => handleCreateKey(org.id, nonce)}
              >
                <span className="workspace-radio">○</span>
                <span className="workspace-name">{org.name}</span>
              </div>
            ))}
          </>
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
        ) : step === 'auth' ? (
          <div className="provision-footer-row">
            <button className="workspace-back-btn" onClick={() => setStep('name')}>BACK</button>
            <button className="workspace-activate-btn" onClick={handleAuthenticate}>
              OPEN BROWSER TO AUTHENTICATE
            </button>
          </div>
        ) : step === 'auth-browser' ? (
          <div className="provision-footer-row">
            <button className="workspace-back-btn" onClick={handleReset}>CANCEL</button>
          </div>
        ) : step === 'auth-org' ? (
          <div className="provision-footer-row">
            <button className="workspace-back-btn" onClick={handleReset}>CANCEL</button>
          </div>
        ) : (
          <div className="provision-footer-row">
            <button
              className="workspace-back-btn"
              onClick={onBack}
              disabled={step !== 'name'}
            >
              BACK
            </button>
            <button
              className="workspace-activate-btn"
              onClick={handleConnect}
              disabled={!deviceName.trim() || step !== 'name'}
            >
              CONTINUE
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
