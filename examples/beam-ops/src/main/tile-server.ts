import express from 'express';
import cors from 'cors';
import { join } from 'path';
import { existsSync, readFileSync } from 'fs';
import http from 'http';
import type { Database } from 'sql.js';
import { getSqlJs } from './sql-init';

const TILE_SERVER_PORT = 3002;

let server: http.Server | null = null;
let db: Database | null = null;

export interface MbtilesMetadata {
  center: [number, number] | null;
  zoom: number | null;
  name: string;
}

function readMbtilesMetadata(database: Database, filePath: string): MbtilesMetadata {
  const fileName = filePath.split('/').pop()?.replace(/\.mbtiles$/i, '') ?? filePath;
  try {
    const stmt = database.prepare('SELECT name, value FROM metadata');
    const meta: Record<string, string> = {};
    while (stmt.step()) {
      const row = stmt.getAsObject() as { name: string; value: string };
      meta[row.name] = row.value;
    }
    stmt.free();

    let center: [number, number] | null = null;
    let zoom: number | null = null;

    if (meta.center) {
      const parts = meta.center.split(',').map(Number);
      if (parts.length >= 2 && !isNaN(parts[0]) && !isNaN(parts[1])) {
        center = [parts[0], parts[1]];
        if (parts.length >= 3 && !isNaN(parts[2])) zoom = parts[2];
      }
    }

    if (!center && meta.bounds) {
      const b = meta.bounds.split(',').map(Number);
      if (b.length === 4 && b.every((n) => !isNaN(n))) {
        center = [(b[0] + b[2]) / 2, (b[1] + b[3]) / 2];
      }
    }

    if (zoom === null && meta.minzoom) {
      const min = parseInt(meta.minzoom, 10);
      const max = meta.maxzoom ? parseInt(meta.maxzoom, 10) : min;
      zoom = Math.round((min + max) / 2);
    }

    return { center, zoom, name: meta.name ?? fileName };
  } catch {
    return { center: null, zoom: null, name: fileName };
  }
}

export async function switchMbtiles(filePath: string): Promise<MbtilesMetadata> {
  if (db) {
    try { db.close(); } catch { /* ignore */ }
    db = null;
  }

  const fileName = filePath.split('/').pop()?.replace(/\.mbtiles$/i, '') ?? filePath;

  if (!existsSync(filePath)) {
    console.warn('[TileServer] switchMbtiles: file not found:', filePath);
    return { center: null, zoom: null, name: fileName };
  }

  try {
    const SQL = await getSqlJs();
    db = new SQL.Database(readFileSync(filePath));
    console.log('[TileServer] Switched to MBTiles:', filePath);
    return readMbtilesMetadata(db, filePath);
  } catch (err) {
    console.error('[TileServer] Failed to open MBTiles:', err);
    db = null;
    return { center: null, zoom: null, name: fileName };
  }
}

export async function startTileServer(mbtilesPath: string): Promise<void> {
  if (existsSync(mbtilesPath)) {
    try {
      const SQL = await getSqlJs();
      db = new SQL.Database(readFileSync(mbtilesPath));
      console.log('[TileServer] Opened MBTiles:', mbtilesPath);
    } catch (err) {
      console.warn('[TileServer] Could not open MBTiles, tiles will be empty:', err);
    }
  } else {
    console.warn('[TileServer] MBTiles not found at:', mbtilesPath);
    console.warn('[TileServer] Run `npm run tiles:create` to download tiles.');
  }

  const expressApp = express();
  expressApp.use(cors({ origin: '*', methods: ['GET'], allowedHeaders: ['Content-Type'] }));

  expressApp.get('/health', (_req, res) => {
    res.json({ status: 'OK', mbtiles: mbtilesPath, dbOpen: db !== null });
  });

  expressApp.get('/tiles/:z/:x/:y', (req, res) => {
    const z = parseInt(req.params.z, 10);
    const x = parseInt(req.params.x, 10);
    const y = parseInt(req.params.y, 10);

    if (isNaN(z) || isNaN(x) || isNaN(y)) {
      res.status(400).send('Invalid tile coordinates');
      return;
    }

    if (!db) {
      res.status(204).end();
      return;
    }

    try {
      // MBTiles uses TMS (origin bottom-left), MapLibre uses XYZ (origin top-left)
      const tmsY = (1 << z) - 1 - y;
      const stmt = db.prepare(
        'SELECT tile_data FROM tiles WHERE zoom_level = ? AND tile_column = ? AND tile_row = ?'
      );
      stmt.bind([z, x, tmsY]);
      if (stmt.step()) {
        const row = stmt.getAsObject() as { tile_data: Uint8Array | null };
        stmt.free();
        if (!row.tile_data) {
          res.status(204).end();
          return;
        }
        res.set('Content-Type', 'image/png');
        res.set('Cache-Control', 'public, max-age=86400');
        res.send(Buffer.from(row.tile_data));
      } else {
        stmt.free();
        res.status(204).end();
      }
    } catch (err) {
      console.error('[TileServer] Error reading tile:', err);
      res.status(500).send('Tile read error');
    }
  });

  return new Promise((resolve, reject) => {
    server = expressApp.listen(TILE_SERVER_PORT, '127.0.0.1', () => {
      console.log(`[TileServer] Listening on http://127.0.0.1:${TILE_SERVER_PORT}`);
      resolve();
    });
    server.on('error', (err: NodeJS.ErrnoException) => {
      if (err.code === 'EADDRINUSE') {
        console.log(`[TileServer] Port ${TILE_SERVER_PORT} already in use, reusing existing server`);
        server = null;
        resolve();
      } else {
        console.error('[TileServer] Server error:', err);
        reject(err);
      }
    });
  });
}

export function stopTileServer(): void {
  if (db) {
    db.close();
    db = null;
  }
  if (server) {
    server.close();
    server = null;
  }
}
