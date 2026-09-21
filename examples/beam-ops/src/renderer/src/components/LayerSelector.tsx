import { useState } from 'react';

interface LayerSelectorProps {
  currentLayer: string;
  onLayerLoaded: (meta: { center: [number, number] | null; zoom: number | null; name: string }) => void;
}

export default function LayerSelector({ currentLayer, onLayerLoaded }: LayerSelectorProps) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleLoad = async () => {
    setLoading(true);
    setError(null);
    try {
      const filePath = await window.tileApi.openDialog();
      if (!filePath) return;
      const meta = await window.tileApi.load(filePath);
      onLayerLoaded(meta);
    } catch (err) {
      setError('Failed to load layer');
      console.error('[LayerSelector]', err);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="layer-selector">
      <div className="layer-selector-header">LAYERS</div>
      <div className="layer-selector-item">
        <span className="layer-active-dot" />
        <span className="layer-name" title={currentLayer}>{currentLayer}</span>
      </div>
      {error && <div className="layer-error">{error}</div>}
      <button
        className="layer-load-btn"
        onClick={handleLoad}
        disabled={loading}
      >
        {loading ? 'LOADING...' : '+ LOAD LAYER'}
      </button>
    </div>
  );
}
