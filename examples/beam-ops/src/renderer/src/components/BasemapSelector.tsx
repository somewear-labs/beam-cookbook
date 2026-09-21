export interface BasemapOption {
  id: string;
  label: string;
  mapboxStyle?: string;
}

export const BASEMAP_OPTIONS: BasemapOption[] = [
  { id: 'local', label: 'TACTICAL' },
  { id: 'satellite', label: 'SATELLITE', mapboxStyle: 'mapbox/satellite-v9' },
  { id: 'satellite-streets', label: 'SAT+ROADS', mapboxStyle: 'mapbox/satellite-streets-v12' },
  { id: 'outdoors', label: 'TERRAIN', mapboxStyle: 'mapbox/outdoors-v12' },
  { id: 'dark', label: 'DARK', mapboxStyle: 'mapbox/dark-v11' },
];

interface Props {
  active: string;
  hasToken: boolean;
  onChange: (id: string) => void;
}

export default function BasemapSelector({ active, hasToken, onChange }: Props) {
  return (
    <div className="basemap-selector">
      <div className="basemap-options">
        {BASEMAP_OPTIONS.map((opt) => (
          <button
            key={opt.id}
            className={`basemap-btn${active === opt.id ? ' active' : ''}`}
            onClick={() => onChange(opt.id)}
            disabled={opt.mapboxStyle !== undefined && !hasToken}
            title={opt.mapboxStyle && !hasToken ? 'No Mapbox token' : opt.label}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  );
}
