import type { Database } from 'sql.js';
import { getSqlJs } from './sql-init';
import { app } from 'electron';
import { join } from 'path';
import { readFileSync, writeFileSync, existsSync } from 'fs';

export interface PersistedLayer {
  filePath: string;
  name: string;
  centerLng: number | null;
  centerLat: number | null;
  zoom: number | null;
}

let db: Database | null = null;
let dbPath: string | null = null;

async function getDb(): Promise<Database> {
  if (db) return db;
  const SQL = await getSqlJs();
  dbPath = join(app.getPath('userData'), 'beam-ops-state.db');
  const existing = existsSync(dbPath) ? readFileSync(dbPath) : null;
  db = new SQL.Database(existing ?? undefined);
  db.run(`
    CREATE TABLE IF NOT EXISTS tile_layers (
      file_path  TEXT    PRIMARY KEY,
      name       TEXT    NOT NULL,
      center_lng REAL,
      center_lat REAL,
      zoom       REAL,
      added_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
    )
  `);
  if (!existing) persist();
  return db;
}

function persist(): void {
  writeFileSync(dbPath!, Buffer.from(db!.export()));
}

export async function listLayers(): Promise<PersistedLayer[]> {
  const database = await getDb();
  const stmt = database.prepare(
    'SELECT file_path, name, center_lng, center_lat, zoom FROM tile_layers ORDER BY added_at DESC'
  );
  const rows: PersistedLayer[] = [];
  while (stmt.step()) {
    const r = stmt.getAsObject() as {
      file_path: string;
      name: string;
      center_lng: number | null;
      center_lat: number | null;
      zoom: number | null;
    };
    rows.push({ filePath: r.file_path, name: r.name, centerLng: r.center_lng, centerLat: r.center_lat, zoom: r.zoom });
  }
  stmt.free();
  return rows;
}

export async function upsertLayer(layer: PersistedLayer): Promise<void> {
  const database = await getDb();
  database.run(
    `INSERT INTO tile_layers (file_path, name, center_lng, center_lat, zoom, added_at)
     VALUES (?, ?, ?, ?, ?, strftime('%s', 'now'))
     ON CONFLICT(file_path) DO UPDATE SET
       name       = excluded.name,
       center_lng = excluded.center_lng,
       center_lat = excluded.center_lat,
       zoom       = excluded.zoom,
       added_at   = excluded.added_at`,
    [layer.filePath, layer.name, layer.centerLng, layer.centerLat, layer.zoom]
  );
  persist();
}

export async function removeLayer(filePath: string): Promise<void> {
  const database = await getDb();
  database.run('DELETE FROM tile_layers WHERE file_path = ?', [filePath]);
  persist();
}

export function closeLayerStore(): void {
  if (db) {
    db.close();
    db = null;
  }
}
