import { contextBridge, ipcRenderer } from 'electron';

// ─── Type definitions shared between preload and renderer ─────────────────────

export interface AuthStateResponse {
  state: 'SignedIn' | 'SignedOut' | 'Unknown';
  hasApiKey: boolean;
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
  version?: string;
  uptime?: number;
  activeWorkspaceId?: string;
  [key: string]: unknown;
}

export interface PayloadEvent {
  id?: string;
  timestamp?: number;
  type?: string;
  content?: unknown;
  senderId?: string;
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

// ─── Beam API exposed to renderer ─────────────────────────────────────────────

const beamApi = {
  /**
   * Check if the Beam daemon is reachable and healthy.
   */
  health: (): Promise<boolean> => ipcRenderer.invoke('beam:health'),

  /**
   * Get authentication state from the Beam daemon.
   */
  authState: (): Promise<AuthStateResponse> => ipcRenderer.invoke('beam:auth-state'),

  /**
   * Set the API key for authentication.
   */
  setApiKey: (apiKey: string): Promise<{ success: boolean; message: string; isAuthenticated: boolean }> =>
    ipcRenderer.invoke('beam:set-api-key', apiKey),

  /**
   * Get system info (includes active workspace).
   */
  system: (): Promise<SystemInfo> => ipcRenderer.invoke('beam:system'),

  /**
   * List all workspaces.
   */
  workspaces: (): Promise<WorkspaceListResponse> => ipcRenderer.invoke('beam:workspaces'),

  /**
   * Activate a workspace by ID.
   */
  activateWorkspace: (workspaceId: string): Promise<ActivateWorkspaceResponse> =>
    ipcRenderer.invoke('beam:activate-workspace', workspaceId),

  /**
   * Connect to a USB node device.
   */
  deviceConnect: (port?: string): Promise<boolean> =>
    ipcRenderer.invoke('beam:device-connect', port),

  /**
   * Disconnect from the USB node device.
   */
  deviceDisconnect: (): Promise<boolean> =>
    ipcRenderer.invoke('beam:device-disconnect'),

  /**
   * Get all identities in the current workspace.
   */
  identities: (): Promise<IdentityDTO[]> => ipcRenderer.invoke('beam:identities'),

  /**
   * Get recent payloads.
   */
  payloads: (): Promise<PayloadEvent[]> => ipcRenderer.invoke('beam:payloads'),

  /**
   * Subscribe to live payload tail (SSE via IPC).
   * Returns a cleanup function that stops the tail.
   */
  tailPayloads: (callback: (payload: PayloadEvent) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: PayloadEvent) => {
      callback(payload);
    };
    ipcRenderer.on('beam:payload-event', handler);
    ipcRenderer.send('beam:tail-payloads');

    return () => {
      ipcRenderer.removeListener('beam:payload-event', handler);
      ipcRenderer.send('beam:stop-tail');
    };
  },

  /**
   * Get current mesh network state snapshot.
   */
  network: (): Promise<NetworkStateDTO> => ipcRenderer.invoke('beam:network'),

  /**
   * Subscribe to live network state updates (SSE via IPC).
   * Returns a cleanup function that stops the tail.
   */
  tailNetwork: (callback: (state: NetworkStateDTO) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, state: NetworkStateDTO) => {
      callback(state);
    };
    ipcRenderer.on('beam:network-event', handler);
    ipcRenderer.send('beam:tail-network');

    return () => {
      ipcRenderer.removeListener('beam:network-event', handler);
      ipcRenderer.send('beam:stop-network-tail');
    };
  },

  /**
   * Subscribe to live satellite signal quality updates (SSE via IPC).
   * Returns a cleanup function that stops the tail.
   */
  tailSatelliteQuality: (callback: (q: SatQuality) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, q: SatQuality) => {
      callback(q);
    };
    ipcRenderer.on('beam:satellite-quality-event', handler);
    ipcRenderer.send('beam:tail-satellite-quality');

    return () => {
      ipcRenderer.removeListener('beam:satellite-quality-event', handler);
      ipcRenderer.send('beam:stop-satellite-quality-tail');
    };
  },

  /**
   * Provision an edge compute device. Returns the created workspace on success.
   */
  provisionEdge: (name: string): Promise<WorkspaceInfo> =>
    ipcRenderer.invoke('beam:provision-edge', name),

  /**
   * Validate a Somewear server host. Returns appUrl + nonce on success.
   */
  validateServer: (host: string): Promise<{ valid: boolean; appUrl?: string; nonce?: string; error?: string }> =>
    ipcRenderer.invoke('beam:validate-server', host),

  /**
   * Open a URL in the system browser.
   */
  openExternal: (url: string): Promise<void> =>
    ipcRenderer.invoke('beam:open-external', url),

  /**
   * Single check for an auth token by nonce. Returns null token if not yet available.
   */
  checkAuthToken: (nonce: string): Promise<{ token: string | null }> =>
    ipcRenderer.invoke('beam:check-auth-token', nonce),

  /**
   * Fetch organizations available for the authenticated nonce.
   */
  fetchOrganizations: (nonce: string): Promise<{ organizations: Array<{ id: string; name: string }> }> =>
    ipcRenderer.invoke('beam:fetch-organizations', nonce),

  /**
   * Create an API key for the given organization.
   */
  createApiKey: (organizationId: string, nonce: string): Promise<{ success: boolean; message: string }> =>
    ipcRenderer.invoke('beam:create-api-key', organizationId, nonce),

  /**
   * Fetch current outbound queue items, optionally filtered by channel.
   */
  queue: (channel?: string): Promise<QueueItem[]> =>
    ipcRenderer.invoke('beam:queue', channel),

  /**
   * Cancel a pending outbound package by parcel ID.
   */
  cancelPackage: (parcelId: number, channel?: string): Promise<void> =>
    ipcRenderer.invoke('beam:cancel-package', parcelId, channel),

  flushQueue: (channel?: string): Promise<{ flushed: number; total: number }> =>
    ipcRenderer.invoke('beam:flush-queue', channel),
};

// ─── RPC API exposed to renderer ─────────────────────────────────────────────

const rpcApi = {
  /** Spawn rpc shell for the given workspace, or a specific user when targetUserId is provided. */
  start: (workspaceId: number, targetUserId?: number): Promise<void> =>
    ipcRenderer.invoke('rpc:start', workspaceId, targetUserId),

  /** Write a command line to the shell's stdin. */
  send: (command: string): Promise<void> =>
    ipcRenderer.invoke('rpc:send', command),

  /** Kill the shell subprocess. */
  stop: (): Promise<void> =>
    ipcRenderer.invoke('rpc:stop'),

  /** Subscribe to stdout lines from the shell. Returns an unsubscribe fn. */
  onOutput: (cb: (line: string) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, line: string) => cb(line);
    ipcRenderer.on('rpc:output', h);
    return () => ipcRenderer.removeListener('rpc:output', h);
  },

  /** Subscribe to shell exit. Returns an unsubscribe fn. */
  onExit: (cb: (code: number | null) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, code: number | null) => cb(code);
    ipcRenderer.on('rpc:exit', h);
    return () => ipcRenderer.removeListener('rpc:exit', h);
  },

  /** Subscribe to spawn errors. Returns an unsubscribe fn. */
  onError: (cb: (msg: string) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, msg: string) => cb(msg);
    ipcRenderer.on('rpc:error', h);
    return () => ipcRenderer.removeListener('rpc:error', h);
  }
};

// ─── Tile API exposed to renderer ─────────────────────────────────────────────

const tileApi = {
  tileServerUrl: 'http://localhost:3002',

  openDialog: (): Promise<string | null> =>
    ipcRenderer.invoke('tiles:open-dialog'),

  load: (filePath: string): Promise<{ center: [number, number] | null; zoom: number | null; name: string }> =>
    ipcRenderer.invoke('tiles:load', filePath)
};

// ─── Layer persistence API exposed to renderer ────────────────────────────────

interface PersistedLayer {
  filePath: string;
  name: string;
  centerLng: number | null;
  centerLat: number | null;
  zoom: number | null;
}

const layersApi = {
  list: (): Promise<PersistedLayer[]> =>
    ipcRenderer.invoke('layers:list'),

  upsert: (layer: PersistedLayer): Promise<void> =>
    ipcRenderer.invoke('layers:upsert', layer),

  remove: (filePath: string): Promise<void> =>
    ipcRenderer.invoke('layers:remove', filePath)
};

// ─── Config API exposed to renderer ──────────────────────────────────────────

const configApi = {
  getMapboxToken: (): Promise<string | null> =>
    ipcRenderer.invoke('config:mapbox-token')
};

// ─── Edge Compute API exposed to renderer ────────────────────────────────────

interface EdgeConfig {
  port: number;
  workspaceId: number;
}

const edgeApi = {
  loadConfig: (): Promise<EdgeConfig> =>
    ipcRenderer.invoke('edge:load-config'),

  status: (): Promise<{ running: boolean }> =>
    ipcRenderer.invoke('edge:status'),

  start: (port: number, workspaceId: number): Promise<void> =>
    ipcRenderer.invoke('edge:start', port, workspaceId),

  stop: (): Promise<void> =>
    ipcRenderer.invoke('edge:stop'),

  onOutput: (cb: (line: string) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, line: string) => cb(line);
    ipcRenderer.on('edge:output', h);
    return () => ipcRenderer.removeListener('edge:output', h);
  },

  onExit: (cb: (code: number | null) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, code: number | null) => cb(code);
    ipcRenderer.on('edge:exit', h);
    return () => ipcRenderer.removeListener('edge:exit', h);
  },

  onError: (cb: (msg: string) => void): (() => void) => {
    const h = (_: Electron.IpcRendererEvent, msg: string) => cb(msg);
    ipcRenderer.on('edge:error', h);
    return () => ipcRenderer.removeListener('edge:error', h);
  }
};

// ─── Expose APIs via contextBridge ────────────────────────────────────────────

contextBridge.exposeInMainWorld('beamApi', beamApi);
contextBridge.exposeInMainWorld('rpcApi', rpcApi);
contextBridge.exposeInMainWorld('tileApi', tileApi);
contextBridge.exposeInMainWorld('layersApi', layersApi);
contextBridge.exposeInMainWorld('configApi', configApi);
contextBridge.exposeInMainWorld('edgeApi', edgeApi);

// ─── Type augmentation for TypeScript in renderer ────────────────────────────

declare global {
  interface Window {
    beamApi: typeof beamApi & {
      queue: (channel?: string) => Promise<QueueItem[]>;
      cancelPackage: (parcelId: number, channel?: string) => Promise<void>;
      flushQueue: () => Promise<{ flushed: number; total: number }>;
      validateServer: (host: string) => Promise<{ valid: boolean; appUrl?: string; nonce?: string; error?: string }>;
      openExternal: (url: string) => Promise<void>;
      checkAuthToken: (nonce: string) => Promise<{ token: string | null }>;
      fetchOrganizations: (nonce: string) => Promise<{ organizations: Array<{ id: string; name: string }> }>;
      createApiKey: (organizationId: string, nonce: string) => Promise<{ success: boolean; message: string }>;
    };
    rpcApi: {
      start: (workspaceId: number, targetUserId?: number) => Promise<void>;
      send: (command: string) => Promise<void>;
      stop: () => Promise<void>;
      onOutput: (cb: (line: string) => void) => () => void;
      onExit: (cb: (code: number | null) => void) => () => void;
      onError: (cb: (msg: string) => void) => () => void;
    };
    tileApi: typeof tileApi;
    layersApi: typeof layersApi;
    configApi: typeof configApi;
    edgeApi: typeof edgeApi;
  }
}
