import {
  beamApi,
  AuthStateResponse,
  SystemInfo,
  WorkspaceInfo,
  WorkspaceListResponse,
  PayloadEvent
} from '../services/beamApi';

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export type BeamControllerEvent =
  | 'connectionStateChange'
  | 'workspaceChange'
  | 'authStateChange'
  | 'systemChange'
  | 'initStatus'
  | 'error';

type EventListener<T = unknown> = (data: T) => void;

/**
 * BeamController manages the connection to the Beam daemon and all workspace
 * lifecycle operations. It is intentionally framework-agnostic so it can be
 * used directly in React via useState/useEffect without needing a context.
 */
export class BeamController {
  private _connectionState: ConnectionState = 'disconnected';
  private _activeWorkspace: WorkspaceInfo | null = null;
  private _authState: AuthStateResponse | null = null;
  private _systemInfo: SystemInfo | null = null;
  private _listeners: Map<BeamControllerEvent, EventListener[]> = new Map();
  private _pollTimer: ReturnType<typeof setInterval> | null = null;

  // ─── Public getters ──────────────────────────────────────────────────────

  get connectionState(): ConnectionState {
    return this._connectionState;
  }

  get activeWorkspace(): WorkspaceInfo | null {
    return this._activeWorkspace;
  }

  get authState(): AuthStateResponse | null {
    return this._authState;
  }

  get systemInfo(): SystemInfo | null {
    return this._systemInfo;
  }

  // ─── Event emitter ───────────────────────────────────────────────────────

  on(event: BeamControllerEvent, listener: EventListener): void {
    if (!this._listeners.has(event)) {
      this._listeners.set(event, []);
    }
    this._listeners.get(event)!.push(listener);
  }

  off(event: BeamControllerEvent, listener: EventListener): void {
    const listeners = this._listeners.get(event) ?? [];
    this._listeners.set(
      event,
      listeners.filter((l) => l !== listener)
    );
  }

  private emit(event: BeamControllerEvent, data?: unknown): void {
    const listeners = this._listeners.get(event) ?? [];
    listeners.forEach((l) => l(data));
  }

  // ─── State transitions ───────────────────────────────────────────────────

  private setConnectionState(state: ConnectionState): void {
    if (this._connectionState !== state) {
      this._connectionState = state;
      this.emit('connectionStateChange', state);
    }
  }

  // ─── Core lifecycle ───────────────────────────────────────────────────────

  /**
   * Initialize: check health → check auth → load workspaces.
   * Sets connectionState to 'connected' if everything succeeds.
   */
  async initialize(): Promise<void> {
    this.setConnectionState('connecting');

    const step = async (msg: string, work: () => Promise<void>) => {
      this.emit('initStatus', msg);
      const [result] = await Promise.allSettled([work(), new Promise(r => setTimeout(r, 400))]);
      if (result.status === 'rejected') throw result.reason;
    };

    try {
      // 1. Health check
      let healthy = false;
      await step('CHECKING DAEMON...', async () => { healthy = await beamApi.health(); });
      if (!healthy) {
        this.setConnectionState('error');
        this.emit('error', new Error('Beam daemon is not responding'));
        return;
      }

      // 2. Auth state
      let authState!: Awaited<ReturnType<typeof beamApi.authState>>;
      await step('CHECKING AUTH...', async () => {
        authState = await beamApi.authState();
        this._authState = authState;
        this.emit('authStateChange', authState);
      });

      if (authState.state !== 'SignedIn') {
        this.setConnectionState('error');
        this.emit('error', new Error('Beam daemon is not authenticated'));
        return;
      }

      // 3. Attempt device connection — best-effort, non-blocking on failure
      beamApi.deviceConnect().catch(() => {});

      // 4. System info — best-effort
      beamApi.system().then((sysInfo) => {
        this._systemInfo = sysInfo;
        this.emit('systemChange', sysInfo);
      }).catch(() => {});

      // 5. Load workspaces to find active one (retry on 5xx — daemon may still be connecting to backend)
      let wsResponse!: Awaited<ReturnType<typeof beamApi.workspaces>>;
      await step('LOADING WORKSPACES...', async () => {
        const delays = [1000, 2000, 4000];
        for (let attempt = 0; ; attempt++) {
          try {
            wsResponse = await beamApi.workspaces();
            break;
          } catch (err) {
            if (attempt >= delays.length) throw err;
            await new Promise(r => setTimeout(r, delays[attempt]));
          }
        }
      });
      const activeWs = wsResponse.workspaces.find(
        (ws) => ws.workspaceId === wsResponse.activeWorkspaceId || ws.isActive
      ) ?? null;

      this._activeWorkspace = activeWs;
      this.emit('workspaceChange', activeWs);

      this.setConnectionState('connected');

      // 6. Start polling for live updates
      this.startPolling();
    } catch (err) {
      console.error('[BeamController] Initialize error:', err);
      this.setConnectionState('error');
      this.emit('error', err);
    }
  }

  private startPolling(): void {
    this.stopPolling();
    this._pollTimer = setInterval(async () => {
      if (this._connectionState !== 'connected') return;
      try {
        const healthy = await beamApi.health();
        if (!healthy) {
          this.setConnectionState('error');
          this.stopPolling();
          return;
        }
        const wsResponse = await beamApi.workspaces();
        const activeWs =
          wsResponse.workspaces.find(
            (ws) => ws.workspaceId === wsResponse.activeWorkspaceId || ws.isActive
          ) ?? null;
        if (activeWs?.workspaceId !== this._activeWorkspace?.workspaceId) {
          this._activeWorkspace = activeWs;
          this.emit('workspaceChange', activeWs);
        }
      } catch {
        // Non-fatal: retry next tick
      }
    }, 10_000);
  }

  private stopPolling(): void {
    if (this._pollTimer !== null) {
      clearInterval(this._pollTimer);
      this._pollTimer = null;
    }
  }

  /**
   * Activate a workspace:
   *  1. POST /api/device/connect (connect USB node — non-fatal if fails)
   *  2. POST /api/workspace/activate
   */
  async activateWorkspace(id: string): Promise<void> {
    if (this._connectionState === 'error') {
      throw new Error('Cannot activate workspace: Beam daemon not connected');
    }

    // Step 1: Connect node (best-effort)
    try {
      await beamApi.deviceConnect();
    } catch (err) {
      console.warn('[BeamController] device/connect failed (non-fatal):', err);
    }

    // Step 2: Activate workspace
    const result = await beamApi.activateWorkspace(id);
    if (!result.success) {
      throw new Error(`Failed to activate workspace ${id}`);
    }

    this._activeWorkspace = result.workspace;
    this.emit('workspaceChange', result.workspace);
  }

  /**
   * List all available workspaces.
   */
  async listWorkspaces(): Promise<WorkspaceListResponse> {
    return beamApi.workspaces();
  }

  /**
   * Subscribe to live payload SSE tail via IPC.
   * Returns a cleanup function.
   */
  tailPayloads(cb: (payload: PayloadEvent) => void): () => void {
    return beamApi.tailPayloads(cb);
  }

  /**
   * Reset controller state (e.g., on logout).
   */
  reset(): void {
    this.stopPolling();
    this._connectionState = 'disconnected';
    this._activeWorkspace = null;
    this._authState = null;
    this._listeners.clear();
  }
}

// Singleton instance for use throughout the app
export const beamController = new BeamController();
