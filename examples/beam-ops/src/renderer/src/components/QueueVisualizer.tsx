import { useCallback, useEffect, useRef, useState } from 'react';
import { beamApi, QueueItem } from '../services/beamApi';

const POLL_INTERVAL_MS = 750;
const FLASH_DURATION_MS = 1000;
const CHANNELS = ['Satellite', 'Radio', 'Cellular'] as const;
type Channel = typeof CHANNELS[number];
const CHANNEL_LABELS: Record<Channel, string> = {
  Satellite: 'SAT',
  Radio:     'RADIO',
  Cellular:  'CELL',
};

// Mesh is deprecated in favour of Radio — treat them as the same channel
function normalizeChannel(ch: string): Channel {
  if (ch === 'Mesh') return 'Radio';
  return ch as Channel;
}

// Priority labels matching beam's DevicePayload.Priority constants
function priorityLabel(priority: number): string {
  if (priority >= 100) return 'SOS';
  if (priority >= 99)  return 'CANCEL';
  if (priority >= 98)  return 'REG';
  if (priority >= 12)  return `MSG(${priority})`;
  if (priority >= 10)  return `TRK(${priority})`;
  return String(priority);
}

function formatAge(isoDate: string): string {
  const ms = Date.now() - new Date(isoDate).getTime();
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s`;
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m`;
  return `${Math.floor(ms / 3_600_000)}h`;
}

interface FlashingItem extends QueueItem {
  flashUntil: number;
}

export default function QueueVisualizer() {
  const [items, setItems] = useState<QueueItem[]>([]);
  const [flashing, setFlashing] = useState<Map<string, FlashingItem>>(new Map());
  const [activeChannel, setActiveChannel] = useState<Channel>('Satellite');
  const [cancelling, setCancelling] = useState<Set<number>>(new Set());
  const [flushing, setFlushing] = useState<Set<Channel>>(new Set());
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const prevItemsRef = useRef<Map<string, QueueItem>>(new Map());
  const flashTimers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());

  const poll = useCallback(async () => {
    try {
      const raw = await beamApi.queue();
      const fresh = raw.map((i) => ({ ...i, channel: normalizeChannel(i.channel) as QueueItem['channel'] }));
      const freshMap = new Map(fresh.map((i) => [i.datagramId, i]));

      // Detect removals → start flash
      const newFlash = new Map(flashing);
      let flashChanged = false;
      for (const [id, item] of prevItemsRef.current) {
        if (!freshMap.has(id) && !newFlash.has(id)) {
          const flashUntil = Date.now() + FLASH_DURATION_MS;
          newFlash.set(id, { ...item, flashUntil });
          flashChanged = true;
          const t = setTimeout(() => {
            setFlashing((prev) => {
              const next = new Map(prev);
              next.delete(id);
              return next;
            });
            flashTimers.current.delete(id);
          }, FLASH_DURATION_MS);
          flashTimers.current.set(id, t);
        }
      }

      prevItemsRef.current = freshMap;
      setItems(fresh);
      if (flashChanged) setFlashing(newFlash);
    } catch {
      // Beam daemon not reachable — leave stale data
    }
  }, [flashing]);

  useEffect(() => {
    poll();
    const interval = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      clearInterval(interval);
      for (const t of flashTimers.current.values()) clearTimeout(t);
    };
  }, []);

  const handleCancel = useCallback(async (item: QueueItem) => {
    setCancelling((prev) => new Set(prev).add(item.parcelId));
    try {
      await beamApi.cancelPackage(item.parcelId, item.channel);
    } catch {
      // Best-effort — next poll will reflect actual state
    } finally {
      setCancelling((prev) => {
        const next = new Set(prev);
        next.delete(item.parcelId);
        return next;
      });
    }
  }, []);

  const handleFlushChannel = useCallback(async (ch: Channel) => {
    setFlushing((prev) => new Set(prev).add(ch));
    try {
      await beamApi.flushQueue(ch);
    } catch {
      // Best-effort — next poll will reflect actual state
    } finally {
      setFlushing((prev) => {
        const next = new Set(prev);
        next.delete(ch);
        return next;
      });
    }
  }, []);

  const channelItems = items.filter((i) => i.channel === activeChannel);
  const channelFlashing = [...flashing.values()].filter((i) => i.channel === activeChannel);
  const displayed = [...channelFlashing, ...channelItems];

  const counts: Record<Channel, number> = {
    Satellite: items.filter((i) => i.channel === 'Satellite').length,
    Radio:     items.filter((i) => i.channel === 'Radio').length,
    Cellular:  items.filter((i) => i.channel === 'Cellular').length,
  };

  return (
    <div className="queue-viz">
      {/* Channel tabs */}
      <div className="queue-tabs">
        {CHANNELS.map((ch) => (
          <button
            key={ch}
            className={`queue-tab${activeChannel === ch ? ' queue-tab--active' : ''}`}
            onClick={() => setActiveChannel(ch)}
          >
            {CHANNEL_LABELS[ch]}
            {counts[ch] > 0 && <span className="queue-tab-count">{counts[ch]}</span>}
          </button>
        ))}
        <button
          className="queue-tab-flush"
          disabled={flushing.has(activeChannel) || counts[activeChannel] === 0}
          onClick={(e) => { e.stopPropagation(); handleFlushChannel(activeChannel); }}
          title={`Clear all ${activeChannel} queue items`}
        >
          {flushing.has(activeChannel) ? '…' : 'CLEAR'}
        </button>
      </div>

      {/* Item list */}
      <div className="queue-list">
        {displayed.length === 0 ? (
          <div className="queue-empty">QUEUE EMPTY</div>
        ) : (
          displayed.map((item) => {
            const isFlash = flashing.has(item.datagramId);
            const isCancelling = cancelling.has(item.parcelId);
            const expanded = !isFlash && expandedId === item.datagramId;
            return (
              <div
                key={item.datagramId}
                className={`queue-item${isFlash ? ' queue-item--flash' : ''}${expanded ? ' queue-item--expanded' : ''}`}
                onClick={() => { if (!isFlash) setExpandedId(expanded ? null : item.datagramId); }}
              >
                <div className="queue-item-row">
                  <div className="queue-item-main">
                    <span className="queue-item-parcel">#{item.parcelId}</span>
                    {item.packageType && (
                      <span className="queue-item-type">{item.packageType}</span>
                    )}
                    <span className="queue-item-meta">
                      {item.transferType === 'Cancel' ? 'CANCEL' : priorityLabel(item.priority)}
                    </span>
                    {item.collapseKey && (
                      <span className="queue-item-collapse">{item.collapseKey}</span>
                    )}
                    <span className="queue-item-age">{formatAge(item.createdDate)}</span>
                  </div>
                  {!isFlash && (
                    <button
                      className="queue-item-delete"
                      disabled={isCancelling}
                      onClick={(e) => { e.stopPropagation(); handleCancel(item); }}
                      title="Cancel this item"
                    >
                      {isCancelling ? '…' : '✕'}
                    </button>
                  )}
                </div>
                {expanded && (
                  <pre className="queue-detail">{JSON.stringify(item, null, 2)}</pre>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
