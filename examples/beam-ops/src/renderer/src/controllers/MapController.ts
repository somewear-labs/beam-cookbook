import type { Map as LibreMap, GeoJSONSource } from 'maplibre-gl';
import { beamApi, PayloadEvent } from '../services/beamApi';

interface TrackPoint {
  userId: string;
  lat: number;
  lng: number;
}

const SOURCE_ID = 'map-tracks';
const LAYER_HALO = 'map-tracks-halo';
const LAYER_DOT = 'map-tracks-dot';

export class MapController {
  private _map: LibreMap | null = null;
  private _tracks = new Map<string, TrackPoint>();
  private _tailCleanup: (() => void) | null = null;

  private readonly _onStyleLoad = () => {
    this._initLayers();
    this._render();
  };

  attach(map: LibreMap): void {
    this.detach();
    this._map = map;
    map.on('style.load', this._onStyleLoad);
    this._initLayers();
    this._tailCleanup = beamApi.tailPayloads((p) => this._onPayload(p));
  }

  detach(): void {
    this._tailCleanup?.();
    this._tailCleanup = null;
    if (this._map) {
      this._map.off('style.load', this._onStyleLoad);
      this._map = null;
    }
  }

  private _initLayers(): void {
    const map = this._map!;
    if (!map.getSource(SOURCE_ID)) {
      map.addSource(SOURCE_ID, {
        type: 'geojson',
        data: { type: 'FeatureCollection', features: [] }
      });
    }
    if (!map.getLayer(LAYER_HALO)) {
      map.addLayer({
        id: LAYER_HALO,
        type: 'circle',
        source: SOURCE_ID,
        paint: {
          'circle-radius': 16,
          'circle-color': '#00ff88',
          'circle-opacity': 0.18,
          'circle-stroke-width': 0
        }
      });
    }
    if (!map.getLayer(LAYER_DOT)) {
      map.addLayer({
        id: LAYER_DOT,
        type: 'circle',
        source: SOURCE_ID,
        paint: {
          'circle-radius': 7,
          'circle-color': '#00ff88',
          'circle-stroke-width': 1.5,
          'circle-stroke-color': '#ffffff',
          'circle-opacity': 0.92
        }
      });
    }
  }

  private _onPayload(payload: PayloadEvent): void {
    const c = payload.content as Record<string, unknown> | null;
    if (!c || c.latitude === undefined || c.longitude === undefined) return;
    const lat = Number(c.latitude);
    const lng = Number(c.longitude);
    if (!isFinite(lat) || !isFinite(lng)) return;
    const userId = String(payload.sourceUserId ?? 'unknown');
    this._tracks.set(userId, { userId, lat, lng });
    this._render();
  }

  private _render(): void {
    const map = this._map;
    if (!map) return;
    const source = map.getSource(SOURCE_ID) as GeoJSONSource | undefined;
    if (!source) return;
    const features: GeoJSON.Feature<GeoJSON.Point>[] = [...this._tracks.values()].map((t) => ({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: [t.lng, t.lat] },
      properties: { userId: t.userId }
    }));
    source.setData({ type: 'FeatureCollection', features });
  }
}

export const mapController = new MapController();
