import { useEffect, useRef } from 'react';
import maplibregl from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import { BASEMAP_OPTIONS } from './BasemapSelector';

// Hood River, OR
const CENTER: [number, number] = [-121.5241, 45.7054];
const INITIAL_ZOOM = 12;
const TILE_SERVER_URL = 'http://localhost:3002';

interface MapViewProps {
  onMapReady?: (map: maplibregl.Map) => void;
  onCoordinatesChange?: (lng: number, lat: number, zoom: number) => void;
  tileVersion?: number;
  flyTarget?: { center: [number, number]; zoom: number } | null;
  basemapId?: string;
  mapboxToken?: string | null;
}

function buildLocalStyle(tileUrl?: string): maplibregl.StyleSpecification {
  const tiles = tileUrl ?? `${TILE_SERVER_URL}/tiles/{z}/{x}/{y}`;
  return {
    version: 8,
    name: 'Tactical Dark',
    sources: {
      'carto-dark': {
        type: 'raster',
        tiles: [tiles],
        tileSize: 256,
        maxzoom: 18,
        attribution: '© Esri, HERE, Garmin, OpenStreetMap'
      } as maplibregl.RasterSourceSpecification
    },
    layers: [
      {
        id: 'bg',
        type: 'background',
        paint: { 'background-color': '#0d0f12' }
      } as maplibregl.BackgroundLayerSpecification,
      {
        id: 'basemap',
        type: 'raster',
        source: 'carto-dark',
        paint: {
          'raster-opacity': 0.9,
          'raster-saturation': -0.2,
          'raster-brightness-max': 0.75
        }
      } as maplibregl.RasterLayerSpecification
    ]
  };
}

function buildMapboxRasterStyle(mapboxStyle: string, token: string): maplibregl.StyleSpecification {
  const tileUrl = `https://api.mapbox.com/styles/v1/${mapboxStyle}/tiles/256/{z}/{x}/{y}?access_token=${token}`;
  return {
    version: 8,
    sources: {
      'mapbox-raster': {
        type: 'raster',
        tiles: [tileUrl],
        tileSize: 256,
        maxzoom: 22,
        attribution: '© Mapbox © OpenStreetMap'
      } as maplibregl.RasterSourceSpecification
    },
    layers: [
      {
        id: 'bg',
        type: 'background',
        paint: { 'background-color': '#0d0f12' }
      } as maplibregl.BackgroundLayerSpecification,
      {
        id: 'basemap',
        type: 'raster',
        source: 'mapbox-raster'
      } as maplibregl.RasterLayerSpecification
    ]
  };
}

export default function MapView({
  onMapReady,
  onCoordinatesChange,
  tileVersion,
  flyTarget,
  basemapId = 'local',
  mapboxToken = null
}: MapViewProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const mapLoadedRef = useRef(false);
  const mapboxTokenRef = useRef<string | null>(mapboxToken);

  // Keep token ref current for use in transformRequest
  useEffect(() => {
    mapboxTokenRef.current = mapboxToken ?? null;
  }, [mapboxToken]);

  // Bust tile cache when a new MBTiles layer is loaded (local style only)
  useEffect(() => {
    if (!tileVersion) return;
    const map = mapRef.current;
    if (!map || !map.isStyleLoaded()) return;
    const source = map.getSource('carto-dark') as maplibregl.RasterTileSource | undefined;
    source?.setTiles([`${TILE_SERVER_URL}/tiles/{z}/{x}/{y}?v=${tileVersion}`]);
  }, [tileVersion]);

  // Fly to new layer center when layer changes
  useEffect(() => {
    if (!flyTarget) return;
    const map = mapRef.current;
    if (!map) return;
    map.flyTo({ center: flyTarget.center, zoom: flyTarget.zoom, duration: 1500 });
  }, [flyTarget]);

  // Switch basemap style
  useEffect(() => {
    const map = mapRef.current;
    if (!map || !mapLoadedRef.current) return;

    if (basemapId === 'local') {
      map.setStyle(buildLocalStyle());
      return;
    }

    const opt = BASEMAP_OPTIONS.find((o) => o.id === basemapId);
    if (opt?.mapboxStyle && mapboxToken) {
      map.setStyle(buildMapboxRasterStyle(opt.mapboxStyle, mapboxToken));
    }
  }, [basemapId, mapboxToken]);

  // Initialize map (once)
  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;

    const initialStyle = (() => {
      if (basemapId !== 'local' && mapboxTokenRef.current) {
        const opt = BASEMAP_OPTIONS.find((o) => o.id === basemapId);
        if (opt?.mapboxStyle) return buildMapboxRasterStyle(opt.mapboxStyle, mapboxTokenRef.current);
      }
      return buildLocalStyle();
    })();

    const map = new maplibregl.Map({
      container: containerRef.current,
      style: initialStyle,
      center: CENTER,
      zoom: INITIAL_ZOOM,
      attributionControl: false,
      transformRequest: (url): maplibregl.RequestParameters => {
        const token = mapboxTokenRef.current;
        if (
          token &&
          !url.includes('access_token=') &&
          (url.includes('api.mapbox.com') ||
            url.includes('tiles.mapbox.com') ||
            url.includes('events.mapbox.com'))
        ) {
          const sep = url.includes('?') ? '&' : '?';
          return { url: `${url}${sep}access_token=${token}` };
        }
        return { url };
      }
    });

    map.addControl(
      new maplibregl.AttributionControl({ compact: true }),
      'bottom-right'
    );

    map.on('load', () => {
      mapLoadedRef.current = true;
      onMapReady?.(map);
      const center = map.getCenter();
      onCoordinatesChange?.(center.lng, center.lat, map.getZoom());
    });

    map.on('move', () => {
      const center = map.getCenter();
      onCoordinatesChange?.(center.lng, center.lat, map.getZoom());
    });

    mapRef.current = map;

    return () => {
      map.remove();
      mapRef.current = null;
      mapLoadedRef.current = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div
      ref={containerRef}
      style={{
        position: 'absolute',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        width: '100%',
        height: '100%'
      }}
    />
  );
}
