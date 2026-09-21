import initSqlJs, { type SqlJsStatic } from 'sql.js';
import { join } from 'path';
import { readFileSync } from 'fs';
import { app } from 'electron';

let sqlPromise: Promise<SqlJsStatic> | null = null;

export function getSqlJs(): Promise<SqlJsStatic> {
  if (!sqlPromise) {
    const wasmPath = join(app.getAppPath(), 'node_modules/sql.js/dist/sql-wasm.wasm');
    sqlPromise = initSqlJs({ wasmBinary: readFileSync(wasmPath) });
  }
  return sqlPromise;
}
