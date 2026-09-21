import { useEffect, useState } from 'react';
import { nodeRepository, NodeRepositoryState, DeviceDTO } from '../repositories/NodeRepository';
import { NetworkMeshNodeDTO } from '../services/beamApi';

function rssiClass(rssi: number): string {
  if (rssi === -1 || rssi === 0) return 'signal-unknown';
  if (rssi >= -70) return 'signal-good';
  if (rssi >= -85) return 'signal-fair';
  return 'signal-poor';
}

function rssiLabel(rssi: number): string {
  if (rssi === -1 || rssi === 0) return '—';
  return `${rssi} dBm`;
}

function hopsLabel(n: number): string {
  if (n === 0) return 'DIRECT';
  return `${n} HOP${n !== 1 ? 'S' : ''}`;
}

function MeshNode({ node }: { node: NetworkMeshNodeDTO }) {
  const name = node.ownerName ?? `UID:${node.userAccountId}`;
  return (
    <div className="mesh-node-item">
      <div className="mesh-node-row">
        <span className="mesh-node-name">
          <span className="mesh-node-dot">▸</span>
          {name}
        </span>
        <span className="mesh-node-meta-right">
          <span className="mesh-node-hops">{hopsLabel(node.numberOfHopsAway)}</span>
          <span className={`mesh-node-rssi ${rssiClass(node.signalRssi)}`}>
            {rssiLabel(node.signalRssi)}
          </span>
        </span>
      </div>
      {(node.nextHopName || node.canBackhaulData) && (
        <div className="mesh-node-sub">
          {node.nextHopName && <span>via {node.nextHopName}</span>}
          {node.canBackhaulData && <span className="mesh-node-backhaul">BH</span>}
        </div>
      )}
    </div>
  );
}

function PrimaryDeviceInfo({ device }: { device: DeviceDTO }) {
  return (
    <div className="primary-device-info">
      {device.firmwareVersion && (
        <div className="status-row">
          <span className="status-label">FW</span>
          <span className="status-value status-muted">{device.firmwareVersion}</span>
        </div>
      )}
      {device.networkFirmwareVersion && (
        <div className="status-row">
          <span className="status-label">NET FW</span>
          <span className="status-value status-muted">{device.networkFirmwareVersion}</span>
        </div>
      )}
      <div className="status-row">
        <span className="status-label">BATTERY</span>
        <span className={`status-value ${device.battery > 20 ? 'status-connected' : 'status-error'}`}>
          {device.battery}%
        </span>
      </div>
      {device.settings.radioChannel && (
        <div className="status-row">
          <span className="status-label">CHANNEL</span>
          <span className="status-value status-muted">{device.settings.radioChannel}</span>
        </div>
      )}
    </div>
  );
}

export default function NodesPanel() {
  const [repoState, setRepoState] = useState<NodeRepositoryState>(nodeRepository.state);

  useEffect(() => {
    return nodeRepository.subscribe(setRepoState);
  }, []);

  const { networkState, primaryDevice } = repoState;
  const meshNodes = networkState?.meshNodes ?? [];
  const connState = networkState?.connectionState ?? '—';
  const isLive = networkState !== null;

  const connCls =
    connState === 'Connected' ? 'status-connected'
    : connState === 'Connecting' ? 'status-warning'
    : 'status-muted';

  return (
    <div className="tactical-status-panel nodes-panel">
      <div className="status-panel-header nodes-panel-header">
        <span>MESH NODES</span>
        <span className={`nodes-live-badge ${isLive ? 'live' : 'offline'}`}>
          {isLive ? '● LIVE' : '○ OFFLINE'}
        </span>
      </div>

      <div className="status-row">
        <span className="status-label">STATUS</span>
        <span className={`status-value ${connCls}`}>{connState.toUpperCase()}</span>
      </div>

      {networkState?.serial && (
        <div className="status-row">
          <span className="status-label">SERIAL</span>
          <span className="status-value status-muted">{networkState.serial}</span>
        </div>
      )}

      {primaryDevice && <PrimaryDeviceInfo device={primaryDevice} />}

      <div className="status-divider" />

      {meshNodes.length === 0 ? (
        <div className="nodes-empty">NO MESH NODES</div>
      ) : (
        <div className="mesh-node-list">
          {meshNodes.map((node) => (
            <MeshNode key={node.userAccountId} node={node} />
          ))}
        </div>
      )}
    </div>
  );
}
