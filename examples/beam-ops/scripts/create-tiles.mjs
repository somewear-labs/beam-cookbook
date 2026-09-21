#!/usr/bin/env node
/**
 * create-tiles.mjs
 *
 * Downloads CARTO Dark Matter tiles for the Hood River, OR area and stores
 * them in an MBTiles SQLite database at tiles/hood-river.mbtiles.
 *
 * Uses the built-in node:sqlite module (Node.js 22.5+).
 *
 * Usage:
 *   npm run tiles:create
 *   node scripts/create-tiles.mjs
 *
 * MBTiles stores tiles in TMS coordinate space (y=0 at south).
 * MapLibre uses XYZ space (y=0 at north), so we convert on write:
 *   tms_y = (1 << z) - 1 - xyz_y
 */

import { DatabaseSync } from 'node:sqlite';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';
import { existsSync, mkdirSync } from 'fs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, '..');
const TILES_DIR = resolve(ROOT, 'tiles');
const OUTPUT_PATH = resolve(TILES_DIR, 'hood-river.mbtiles');

// ─── Coverage: Hood River, OR bounding box ────────────────────────────────────
// SW corner: 45.55°N, 121.85°W
// NE corner: 45.85°N, 121.15°W
const BOUNDS = {
  minLon: -121.85,
  maxLon: -121.15,
  minLat: 45.55,
  maxLat: 45.85
};

const MIN_ZOOM = 8;
const MAX_ZOOM = 15;
const DELAY_MS = 120;

// ESRI World Dark Gray Base — free, no watermark, tactical dark palette
// ESRI URL format: tile/{z}/{row}/{col}  where row=y (from-top), col=x
const TILE_URL = (z, x, y) =>
  `https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/${z}/${y}/${x}`;

// ─── Coordinate math ─────────────────────────────────────────────────────────

function lonToTileX(lon, zoom) {
  return Math.floor(((lon + 180) / 360) * Math.pow(2, zoom));
}

function latToTileY(lat, zoom) {
  const latRad = (lat * Math.PI) / 180;
  return Math.floor(
    ((1 - Math.log(Math.tan(latRad) + 1 / Math.cos(latRad)) / Math.PI) / 2) *
      Math.pow(2, zoom)
  );
}

function xyzToTms(y, zoom) {
  return (1 << zoom) - 1 - y;
}

function getTileRange(zoom) {
  const minX = lonToTileX(BOUNDS.minLon, zoom);
  const maxX = lonToTileX(BOUNDS.maxLon, zoom);
  const minY = latToTileY(BOUNDS.maxLat, zoom); // NE → smaller Y
  const maxY = latToTileY(BOUNDS.minLat, zoom); // SW → larger Y
  return { minX, maxX, minY, maxY };
}

function countTiles() {
  let total = 0;
  for (let z = MIN_ZOOM; z <= MAX_ZOOM; z++) {
    const { minX, maxX, minY, maxY } = getTileRange(z);
    total += (maxX - minX + 1) * (maxY - minY + 1);
  }
  return total;
}

// ─── Download a tile with retry ───────────────────────────────────────────────

async function downloadTile(z, x, y, retries = 3) {
  const url = TILE_URL(z, x, y);
  for (let attempt = 1; attempt <= retries; attempt++) {
    try {
      const res = await fetch(url, {
        headers: { 'User-Agent': 'BeamOps/1.0 (tile-downloader; hood-river)' }
      });
      if (res.status === 404) return null;
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const buf = await res.arrayBuffer();
      return Buffer.from(buf);
    } catch (err) {
      if (attempt === retries) {
        console.warn(`  [WARN] z=${z} x=${x} y=${y}: ${err.message}`);
        return null;
      }
      await sleep(DELAY_MS * attempt * 2);
    }
  }
  return null;
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function progress(current, total, zoom) {
  const pct = Math.floor((current / total) * 100);
  const filled = Math.floor(pct / 5);
  const bar = '█'.repeat(filled) + '░'.repeat(20 - filled);
  process.stdout.write(`\r  [${bar}] ${pct}% (${current}/${total}) z=${zoom}   `);
}

// ─── Main ─────────────────────────────────────────────────────────────────────

async function main() {
  console.log('');
  console.log('╔══════════════════════════════════════════════╗');
  console.log('║  BEAM OPS — Tile Downloader                  ║');
  console.log('║  Source: CARTO Dark Matter                   ║');
  console.log('║  Area:   Hood River, OR                      ║');
  console.log(`║  Zooms:  ${MIN_ZOOM}–${MAX_ZOOM}                                  ║`);
  console.log('╚══════════════════════════════════════════════╝');
  console.log('');

  if (!existsSync(TILES_DIR)) mkdirSync(TILES_DIR, { recursive: true });

  const totalTiles = countTiles();
  console.log(`Total tiles: ${totalTiles}`);
  console.log(`Output: ${OUTPUT_PATH}`);
  console.log('');

  const db = new DatabaseSync(OUTPUT_PATH);

  db.exec('PRAGMA journal_mode=WAL');
  db.exec('PRAGMA synchronous=NORMAL');

  db.exec(`
    CREATE TABLE IF NOT EXISTS metadata (
      name  TEXT NOT NULL,
      value TEXT
    );
    CREATE TABLE IF NOT EXISTS tiles (
      zoom_level  INTEGER NOT NULL,
      tile_column INTEGER NOT NULL,
      tile_row    INTEGER NOT NULL,
      tile_data   BLOB    NOT NULL,
      PRIMARY KEY (zoom_level, tile_column, tile_row)
    );
    CREATE INDEX IF NOT EXISTS tiles_idx
      ON tiles (zoom_level, tile_column, tile_row);
  `);

  db.exec('DELETE FROM metadata');
  const insertMeta = db.prepare('INSERT INTO metadata (name, value) VALUES (?, ?)');
  for (const [k, v] of [
    ['name', 'Hood River Tactical Dark'],
    ['description', 'CARTO Dark Matter tiles for Hood River, OR'],
    ['version', '1'],
    ['type', 'baselayer'],
    ['format', 'png'],
    ['minzoom', String(MIN_ZOOM)],
    ['maxzoom', String(MAX_ZOOM)],
    ['bounds', `${BOUNDS.minLon},${BOUNDS.minLat},${BOUNDS.maxLon},${BOUNDS.maxLat}`],
    ['center', `-121.5241,45.7054,12`]
  ]) {
    insertMeta.run(k, v);
  }

  const insertTile = db.prepare(
    'INSERT OR REPLACE INTO tiles (zoom_level, tile_column, tile_row, tile_data) VALUES (?, ?, ?, ?)'
  );

  let downloaded = 0;
  let skipped = 0;
  let current = 0;
  const startTime = Date.now();

  for (let z = MIN_ZOOM; z <= MAX_ZOOM; z++) {
    const { minX, maxX, minY, maxY } = getTileRange(z);
    const count = (maxX - minX + 1) * (maxY - minY + 1);
    console.log(`\nZoom ${z}: x=[${minX}–${maxX}] y=[${minY}–${maxY}] (${count} tiles)`);

    for (let x = minX; x <= maxX; x++) {
      for (let y = minY; y <= maxY; y++) {
        current++;
        progress(current, totalTiles, z);

        const tileData = await downloadTile(z, x, y);
        if (tileData) {
          insertTile.run(z, x, xyzToTms(y, z), tileData);
          downloaded++;
        } else {
          skipped++;
        }

        await sleep(DELAY_MS);
      }
    }
  }

  db.close();

  const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);

  console.log('\n');
  console.log('╔══════════════════════════════════════════════╗');
  console.log('║  COMPLETE                                    ║');
  console.log(`║  Downloaded: ${String(downloaded).padEnd(30)}║`);
  console.log(`║  Skipped:    ${String(skipped).padEnd(30)}║`);
  console.log(`║  Time:       ${String(elapsed + 's').padEnd(30)}║`);
  console.log('║  Output:     tiles/hood-river.mbtiles        ║');
  console.log('╚══════════════════════════════════════════════╝');
  console.log('');
}

main().catch((err) => {
  console.error('\n[ERROR]', err);
  process.exit(1);
});
