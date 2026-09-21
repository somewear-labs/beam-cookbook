// ─── Type definitions ─────────────────────────────────────────────────────────
// These mirror the types defined in preload/index.ts and are used throughout the renderer.

export interface AuthStateResponse {
  state: 'SignedIn' | 'SignedOut' | 'Unknown';
  hasApiKey: boolean;
}

export interface SetApiKeyResponse {
  success: boolean;
  message: string;
  isAuthenticated: boolean;
}

export interface WorkspaceInfo {
  workspaceId: string;
  name: string;
  isActive: boolean;
}

export interface WorkspaceListResponse {
  workspaces: WorkspaceInfo[];
  activeWorkspaceId: string | null;
}

export interface ActivateWorkspaceResponse {
  success: boolean;
  workspace: WorkspaceInfo;
}

export interface IdentityDTO {
  identityId: string;
  userAccountId: string;
  name: string;
  email: string;
  phone: string;
}

export interface SystemInfo {
  version?: { current?: string };
  process?: { uptimeSeconds?: number };
  auth?: {
    state?: string;
    host?: string;
    workspaceId?: string;
    workspaceName?: string;
  };
  [key: string]: unknown;
}

export interface PayloadEvent {
  id?: string;
  timestamp?: number;
  type?: string;
  content?: unknown;
  sourceUserId?: number | string;
  workspaceId?: string | number;
  channel?: string | null;
  datagramId?: string;
  [key: string]: unknown;
}

export interface QueueItem {
  datagramId: string;
  parcelId: number;
  channel: 'Satellite' | 'Radio' | 'Cellular' | string;
  priority: number;
  transferType: 'Default' | 'Cancel';
  createdDate: string;
  collapseKey: string;
  packageType?: string;
}

export interface NetworkMeshNodeDTO {
  userAccountId: number;
  ownerName?: string;
  nextHopName?: string;
  numberOfHopsAway: number;
  signalRssi: number;
  canBackhaulData: boolean;
  timestamp?: string;
}

export interface NetworkWorkspaceDeviceDTO {
  serial: string;
  imei: string;
  userAccountId: string;
  hardwareFlavor: string;
}

export interface NetworkStateDTO {
  serial?: string;
  connectionState: string;
  meshNodes: NetworkMeshNodeDTO[];
  workspaceDevices: NetworkWorkspaceDeviceDTO[];
}

export interface SatQuality {
  quality: number; // 0–5, only meaningful when device is Sending or Listening
  time: number;    // epoch ms
}

export interface ValidateServerResponse {
  valid: boolean;
  appUrl?: string;
  nonce?: string;
  error?: string;
}

export interface OrgItem {
  id: string;
  name: string;
}

// ─── Shared network tail (ref-counted) ───────────────────────────────────────
// Only one IPC connection is kept open regardless of how many components
// subscribe. The first subscriber starts it; the last one stops it.

const _networkSubs = new Set<(state: NetworkStateDTO) => void>();
let _networkTailCleanup: (() => void) | null = null;

function _subscribeNetwork(callback: (state: NetworkStateDTO) => void): () => void {
  _networkSubs.add(callback);
  if (_networkSubs.size === 1) {
    _networkTailCleanup = window.beamApi.tailNetwork((state) => {
      _networkSubs.forEach((cb) => cb(state));
    });
  }
  return () => {
    _networkSubs.delete(callback);
    if (_networkSubs.size === 0) {
      _networkTailCleanup?.();
      _networkTailCleanup = null;
    }
  };
}

// ─── IPC wrapper helpers ──────────────────────────────────────────────────────
// All calls go through window.beamApi which is exposed by the preload script.

export const beamApi = {
  health: (): Promise<boolean> => window.beamApi.health(),

  authState: (): Promise<AuthStateResponse> => window.beamApi.authState(),

  setApiKey: (apiKey: string): Promise<SetApiKeyResponse> => window.beamApi.setApiKey(apiKey),

  system: (): Promise<SystemInfo> => window.beamApi.system(),

  workspaces: (): Promise<WorkspaceListResponse> => window.beamApi.workspaces(),

  activateWorkspace: (workspaceId: string): Promise<ActivateWorkspaceResponse> =>
    window.beamApi.activateWorkspace(workspaceId),

  deviceConnect: (port?: string): Promise<boolean> => window.beamApi.deviceConnect(port),

  deviceDisconnect: (): Promise<boolean> => window.beamApi.deviceDisconnect(),

  identities: (): Promise<IdentityDTO[]> => window.beamApi.identities(),

  payloads: (): Promise<PayloadEvent[]> => window.beamApi.payloads(),

  tailPayloads: (callback: (payload: PayloadEvent) => void): (() => void) =>
    window.beamApi.tailPayloads(callback),

  network: (): Promise<NetworkStateDTO> => window.beamApi.network(),

  tailNetwork: _subscribeNetwork,

  tailSatelliteQuality: (callback: (q: SatQuality) => void): (() => void) =>
    window.beamApi.tailSatelliteQuality(callback),

  provisionEdge: (name: string): Promise<WorkspaceInfo> =>
    window.beamApi.provisionEdge(name),

  validateServer: (host: string): Promise<ValidateServerResponse> =>
    window.beamApi.validateServer(host),

  openExternal: (url: string): Promise<void> =>
    window.beamApi.openExternal(url),

  checkAuthToken: (nonce: string): Promise<{ token: string | null }> =>
    window.beamApi.checkAuthToken(nonce),

  fetchOrganizations: (nonce: string): Promise<{ organizations: OrgItem[] }> =>
    window.beamApi.fetchOrganizations(nonce),

  createApiKey: (organizationId: string, nonce: string): Promise<{ success: boolean; message: string }> =>
    window.beamApi.createApiKey(organizationId, nonce),

  queue: (channel?: string): Promise<QueueItem[]> =>
    window.beamApi.queue(channel),

  cancelPackage: (parcelId: number, channel?: string): Promise<void> =>
    window.beamApi.cancelPackage(parcelId, channel),

  flushQueue: (channel?: string): Promise<{ flushed: number; total: number }> =>
    window.beamApi.flushQueue(channel),
};

// ─── Window type declarations ─────────────────────────────────────────────────

declare global {
  interface Window {
    beamApi: {
      health: () => Promise<boolean>;
      authState: () => Promise<AuthStateResponse>;
      setApiKey: (apiKey: string) => Promise<SetApiKeyResponse>;
      system: () => Promise<SystemInfo>;
      workspaces: () => Promise<WorkspaceListResponse>;
      activateWorkspace: (workspaceId: string) => Promise<ActivateWorkspaceResponse>;
      deviceConnect: (port?: string) => Promise<boolean>;
      deviceDisconnect: () => Promise<boolean>;
      identities: () => Promise<IdentityDTO[]>;
      payloads: () => Promise<PayloadEvent[]>;
      tailPayloads: (callback: (payload: PayloadEvent) => void) => () => void;
      network: () => Promise<NetworkStateDTO>;
      tailNetwork: (callback: (state: NetworkStateDTO) => void) => () => void;
      tailSatelliteQuality: (callback: (q: SatQuality) => void) => () => void;
      provisionEdge: (name: string) => Promise<WorkspaceInfo>;
      validateServer: (host: string) => Promise<ValidateServerResponse>;
      openExternal: (url: string) => Promise<void>;
      checkAuthToken: (nonce: string) => Promise<{ token: string | null }>;
      fetchOrganizations: (nonce: string) => Promise<{ organizations: OrgItem[] }>;
      createApiKey: (organizationId: string, nonce: string) => Promise<{ success: boolean; message: string }>;
      queue: (channel?: string) => Promise<QueueItem[]>;
      cancelPackage: (parcelId: number, channel?: string) => Promise<void>;
      flushQueue: (channel?: string) => Promise<{ flushed: number; total: number }>;
    };
    tileApi: {
      tileServerUrl: string;
    };
  }
}
