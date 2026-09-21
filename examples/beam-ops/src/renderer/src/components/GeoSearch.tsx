import { useState, useRef, useCallback, useEffect } from 'react';

interface MapboxFeature {
  place_name: string;
  text: string;
  center: [number, number]; // [lng, lat]
  place_type: string[];
}

interface Props {
  token: string | null;
  onFlyTo: (lng: number, lat: number, zoom: number) => void;
}

// lat,lng — optional spaces around comma, or space-separated
const COORD_RE = /^\s*(-?\d+(?:\.\d+)?)\s*[,\s]\s*(-?\d+(?:\.\d+)?)\s*$/;

export default function GeoSearch({ token, onFlyTo }: Props) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<MapboxFeature[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const search = useCallback(async (q: string) => {
    const trimmed = q.trim();
    if (!trimmed) {
      setResults([]);
      setOpen(false);
      return;
    }

    // Lat/lng shortcut — resolve instantly without a network call
    const match = trimmed.match(COORD_RE);
    if (match) {
      const lat = parseFloat(match[1]);
      const lng = parseFloat(match[2]);
      if (lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180) {
        setResults([{
          place_name: `${lat.toFixed(6)}, ${lng.toFixed(6)}`,
          text: `${lat.toFixed(6)}, ${lng.toFixed(6)}`,
          center: [lng, lat],
          place_type: ['coordinate'],
        }]);
        setOpen(true);
        setLoading(false);
        return;
      }
    }

    if (!token) {
      console.warn('[GeoSearch] no Mapbox token available');
      return;
    }

    abortRef.current?.abort();
    abortRef.current = new AbortController();
    setLoading(true);

    try {
      const url =
        `https://api.mapbox.com/geocoding/v5/mapbox.places/` +
        `${encodeURIComponent(trimmed)}.json` +
        `?access_token=${token}&limit=7&fuzzyMatch=true`;
      const res = await fetch(url, { signal: abortRef.current.signal });
      if (!res.ok) {
        console.warn('[GeoSearch] Mapbox geocoding error:', res.status, await res.text());
        return;
      }
      const data: { features: MapboxFeature[] } = await res.json();
      setResults(data.features);
      setOpen(data.features.length > 0);
    } catch (err: unknown) {
      if (err instanceof Error && err.name !== 'AbortError') {
        console.warn('[GeoSearch] fetch failed:', err);
      }
    } finally {
      setLoading(false);
    }
  }, [token]);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    setQuery(v);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => search(v), 300);
  };

  const handleSelect = (f: MapboxFeature) => {
    const [lng, lat] = f.center;
    const zoom = f.place_type.includes('coordinate') ? 14
      : f.place_type.includes('poi') ? 15
      : f.place_type.includes('address') ? 15
      : f.place_type.includes('neighborhood') ? 13
      : f.place_type.includes('place') ? 11
      : f.place_type.includes('region') ? 8
      : 10;
    onFlyTo(lng, lat, zoom);
    setOpen(false);
    setQuery(f.text);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      setOpen(false);
      inputRef.current?.blur();
    }
    if (e.key === 'Enter' && results.length > 0) {
      handleSelect(results[0]);
    }
  };

  const handleClear = () => {
    setQuery('');
    setResults([]);
    setOpen(false);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    abortRef.current?.abort();
    inputRef.current?.focus();
  };

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  // Secondary context: country/region from place_name after the primary text
  const secondary = (f: MapboxFeature) => {
    const rest = f.place_name.slice(f.text.length).replace(/^,\s*/, '');
    const parts = rest.split(',');
    return parts.slice(0, 2).join(',').trim();
  };

  return (
    <div className="geo-search" ref={containerRef}>
      <div className="geo-search-row">
        <span className="geo-search-icon">⌖</span>
        <input
          ref={inputRef}
          className="geo-search-input"
          type="text"
          placeholder="SEARCH PLACE OR LAT, LNG"
          value={query}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onFocus={() => results.length > 0 && setOpen(true)}
          spellCheck={false}
          autoComplete="off"
          autoCorrect="off"
        />
        {loading && <span className="geo-search-spinner" />}
        {query && !loading && (
          <button className="geo-search-clear" onClick={handleClear} tabIndex={-1}>✕</button>
        )}
      </div>

      {open && results.length > 0 && (
        <ul className="geo-search-results">
          {results.map((f, i) => (
            <li
              key={i}
              className="geo-search-result"
              onMouseDown={(e) => { e.preventDefault(); handleSelect(f); }}
            >
              <span className="geo-search-result-primary">{f.text}</span>
              {secondary(f) && (
                <span className="geo-search-result-secondary">{secondary(f)}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
