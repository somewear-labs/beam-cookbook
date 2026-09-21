import { beamApi, NetworkStateDTO } from '../services/beamApi';

// ─── Reconnect config ─────────────────────────────────────────────────────────

const RECONNECT_COOLDOWN_SECONDS = 5;

// ─── Types mirroring beam server DevicesRequest.kt ───────────────────────────

export interface DeviceSettings {
  connectionMode?: string;
  automaticTracking?: boolean;
  gpsInterval?: number;
  sendInterval?: number;
  altitude?: boolean;
  speedAndCourse?: boolean;
  gpsInitialFix?: boolean;
  locationBackhaulRate?: number;
  led?: boolean;
  haptics?: boolean;
  batteryReporting?: boolean;
  buttonFunction?: string;
  advertiseBleEnabled?: boolean;
  lowDataMode?: boolean;
  radioChannel?: string;
  radioPowerMode?: string;
  backhaul?: boolean;
  satellite?: boolean;
  radioMode?: string;
  satelliteEncryption?: boolean;
  satelliteStayAwake?: boolean;
  lowBandwidthTrackingMultiplier?: number;
}

export interface DeviceDTO {
  battery: number;
  hardwareFlavor?: string;
  firmwareVersion?: string;
  networkFirmwareVersion?: string;
  model?: string;
  address?: string;
  location?: string;
  serial?: string;
  mappedPortNumber?: number;
  settings: DeviceSettings;
  status: string; // DISCOVERED | CONNECTING | CONNECTED | BOOTLOADER | FAILED
}

/**
 * 'cooldown'    — node just disconnected; waiting for it to recover before reconnecting
 * 'reconnecting' — cooldown elapsed; a deviceConnect() call is in-flight
 * 'idle'        — no auto-reconnect in progress
 */
export type ReconnectPhase = 'idle' | 'cooldown' | 'reconnecting';

export interface NodeRepositoryState {
  networkState: NetworkStateDTO | null;
  devices: DeviceDTO[];
  primaryDevice: DeviceDTO | null;
  reconnectPhase: ReconnectPhase;
  cooldownSecondsRemaining: number;
}

type StateListener = (state: NodeRepositoryState) => void;

// ─── Singleton repository ─────────────────────────────────────────────────────

class NodeRepository {
  private _networkState: NetworkStateDTO | null = null;
  private _listeners = new Set<StateListener>();
  private _started = false;

  // Reconnect state
  private _prevConnectionState: string | null = null;
  private _everConnected = false;
  private _reconnectActive = false; // true while we want retries to keep running
  private _reconnectPhase: ReconnectPhase = 'idle';
  private _cooldownSecondsRemaining = 0;
  private _cooldownTimer: ReturnType<typeof setInterval> | null = null;

  get state(): NodeRepositoryState {
    return {
      networkState: this._networkState,
      devices: [],
      primaryDevice: null,
      reconnectPhase: this._reconnectPhase,
      cooldownSecondsRemaining: this._cooldownSecondsRemaining,
    };
  }

  subscribe(listener: StateListener): () => void {
    this._listeners.add(listener);
    if (!this._started) this._start();
    listener(this.state);
    return () => {
      this._listeners.delete(listener);
    };
  }

  private _emit() {
    const s = this.state;
    this._listeners.forEach((l) => l(s));
  }

  private _handleNetworkUpdate(s: NetworkStateDTO) {
    const prev = this._prevConnectionState;
    const curr = s.connectionState;

    console.log(`[NodeRepo] update: ${curr} (serial: ${s.serial ?? 'null'})`);

    if (curr === 'Connected') {
      this._everConnected = true;
      // Node connected — stop any retry loop in progress.
      if (this._reconnectActive || this._reconnectPhase !== 'idle') {
        console.log('[NodeRepo] Node connected — cancelling auto-reconnect loop');
        this._reconnectActive = false;
        this._cancelCooldown();
      }
    }

    // Detect Connected → Disconnected transition and start the recovery sequence.
    // Guard: only trigger if we've seen at least one Connected state, we were
    // previously Connected, and no reconnect sequence is already in progress.
    if (
      this._everConnected &&
      prev === 'Connected' &&
      curr !== 'Connected' &&
      this._reconnectPhase === 'idle' &&
      !this._reconnectActive
    ) {
      console.warn(
        `[NodeRepo] *** NODE DISCONNECTED *** — starting ${RECONNECT_COOLDOWN_SECONDS}s cooldown to allow node recovery before reconnect`
      );
      this._reconnectActive = true;
      this._startCooldown();
    }

    this._prevConnectionState = curr;
    this._networkState = s;
    this._emit();
  }

  private _startCooldown() {
    this._reconnectPhase = 'cooldown';
    this._cooldownSecondsRemaining = RECONNECT_COOLDOWN_SECONDS;

    this._cooldownTimer = setInterval(() => {
      this._cooldownSecondsRemaining -= 1;
      console.log(
        `[NodeRepo] Auto-reconnect cooldown: ${this._cooldownSecondsRemaining}s remaining — giving node time to recover`
      );

      if (this._cooldownSecondsRemaining <= 0) {
        this._clearCooldownTimer();
        this._attemptReconnect();
      } else {
        this._emit();
      }
    }, 1000);
  }

  private _clearCooldownTimer() {
    if (this._cooldownTimer !== null) {
      clearInterval(this._cooldownTimer);
      this._cooldownTimer = null;
    }
  }

  // Cancel cooldown and stop the retry loop.
  private _cancelCooldown() {
    this._clearCooldownTimer();
    this._reconnectPhase = 'idle';
    this._cooldownSecondsRemaining = 0;
    this._reconnectActive = false;
    // Caller (_handleNetworkUpdate) will emit after this returns.
  }

  private async _attemptReconnect() {
    this._reconnectPhase = 'reconnecting';
    this._cooldownSecondsRemaining = 0;
    console.log('[NodeRepo] *** AUTO-RECONNECT *** — cooldown complete, attempting deviceConnect()');
    this._emit();

    try {
      const success = await beamApi.deviceConnect();
      if (success) {
        console.log('[NodeRepo] Auto-reconnect: deviceConnect() succeeded');
      } else {
        console.warn('[NodeRepo] Auto-reconnect: deviceConnect() returned false — node may still be recovering');
      }
    } catch (err) {
      console.error('[NodeRepo] Auto-reconnect: deviceConnect() threw:', err);
    } finally {
      // Only act if the phase hasn't been changed by a concurrent state
      // transition (e.g. SSE fired "Connected" while our request was in-flight).
      if (this._reconnectPhase === 'reconnecting') {
        if (this._reconnectActive) {
          // Still disconnected — schedule the next retry cycle.
          console.warn(
            `[NodeRepo] Auto-reconnect: node still disconnected — retrying in ${RECONNECT_COOLDOWN_SECONDS}s`
          );
          this._startCooldown();
        } else {
          // _reconnectActive was cleared by SSE "Connected" arriving during this attempt.
          console.log('[NodeRepo] Auto-reconnect loop complete — node is connected');
          this._reconnectPhase = 'idle';
          this._emit();
        }
      }
    }
  }

  private _start() {
    this._started = true;

    // Seed from snapshot so state is available before first tail event.
    // We record the initial state so the first SSE "Disconnected" event
    // (which is a true transition from Connected) can trigger the reconnect.
    beamApi.network()
      .then((s) => {
        console.log('[NodeRepo] snapshot:', s.connectionState, s.serial ?? 'null');
        if (!this._networkState) {
          this._prevConnectionState = s.connectionState;
          if (s.connectionState === 'Connected') this._everConnected = true;
          this._networkState = s;
          this._emit();
        }
      })
      .catch(() => {});

    // Live updates via IPC (main process owns the SSE connection).
    beamApi.tailNetwork((s) => {
      this._handleNetworkUpdate(s);
    });
  }
}

export const nodeRepository = new NodeRepository();
