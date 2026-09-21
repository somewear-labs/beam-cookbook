import { app, BrowserWindow, ipcMain, shell, dialog } from 'electron';
import { join } from 'path';
import { homedir } from 'os';
import { readFileSync } from 'fs';
import { electronApp, optimizer, is } from '@electron-toolkit/utils';
import { startTileServer, stopTileServer, switchMbtiles } from './tile-server';
import { setupRpcHandlers, stopRpc } from './rpc';
import { listLayers, upsertLayer, removeLayer, closeLayerStore, PersistedLayer } from './layer-store';
import { setupBeamHandlers, stopBeamHandlers, cleanupWindowTails } from './beam-ipc';
import { setupEdgeComputeHandlers, stopEdgeCompute } from './edge-compute';

// ─── MBTiles path ─────────────────────────────────────────────────────────────
const MBTILES_PATH = join(app.getAppPath(), 'tiles', 'hood-river.mbtiles');

// ─── Mapbox token ─────────────────────────────────────────────────────────────
// Checks (in order): process env, shell rc files, gradle.properties.
// MAPBOX_DOWNLOADS_TOKEN (sk.*) is download-scoped and won't work for tile API calls.
function readShellExport(key: string): string | null {
  const files = ['.zshenv', '.zprofile', '.zshrc', '.profile', '.bash_profile', '.bashrc'];
  const re = new RegExp(`^export\\s+${key}\\s*=\\s*(.+)$`, 'm');
  for (const f of files) {
    try {
      const content = readFileSync(join(homedir(), f), 'utf-8');
      const m = content.match(re);
      if (m) {
        let val = m[1].trim();
        if ((val.startsWith("'") && val.endsWith("'")) || (val.startsWith('"') && val.endsWith('"'))) {
          val = val.slice(1, -1);
        }
        return val;
      }
    } catch { /* file missing, skip */ }
  }
  return null;
}

function readMapboxToken(): string | null {
  if (process.env.MAPBOX_ACCESS_TOKEN) return process.env.MAPBOX_ACCESS_TOKEN;
  const fromShell = readShellExport('MAPBOX_ACCESS_TOKEN');
  if (fromShell) return fromShell;
  try {
    const propsPath = join(homedir(), '.gradle', 'gradle.properties');
    const content = readFileSync(propsPath, 'utf-8');
    const accessMatch = content.match(/^MAPBOX_ACCESS_TOKEN\s*=\s*(.+)$/m);
    if (accessMatch) return accessMatch[1].trim();
    // Fall back to the downloads token — sk.* has broader scopes and works for geocoding
    const downloadsMatch = content.match(/^MAPBOX_DOWNLOADS_TOKEN\s*=\s*(.+)$/m);
    if (downloadsMatch) return downloadsMatch[1].trim();
  } catch { /* gradle.properties missing */ }
  return null;
}

ipcMain.handle('config:mapbox-token', () => readMapboxToken());

// ─── Tile Layer IPC ───────────────────────────────────────────────────────────

ipcMain.handle('tiles:open-dialog', async () => {
  const result = await dialog.showOpenDialog({
    title: 'Select MBTiles File',
    filters: [{ name: 'MBTiles', extensions: ['mbtiles'] }],
    properties: ['openFile']
  });
  if (result.canceled || result.filePaths.length === 0) return null;
  return result.filePaths[0];
});

ipcMain.handle('tiles:load', async (_event, filePath: string) => {
  return switchMbtiles(filePath);
});

// ─── Layer Persistence IPC ────────────────────────────────────────────────────

ipcMain.handle('layers:list', () => listLayers());

ipcMain.handle('layers:upsert', (_event, layer: PersistedLayer) => upsertLayer(layer));

ipcMain.handle('layers:remove', (_event, filePath: string) => removeLayer(filePath));

// ─── Window creation ──────────────────────────────────────────────────────────

function createWindow(): void {
  const mainWindow = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 1024,
    minHeight: 700,
    backgroundColor: '#090e15',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 16, y: 12 },
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      nodeIntegration: false
    }
  });

  mainWindow.on('ready-to-show', () => {
    mainWindow.show();
  });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });

  mainWindow.on('closed', () => {
    cleanupWindowTails(mainWindow.webContents.id);
  });

  if (is.dev && process.env['ELECTRON_RENDERER_URL']) {
    mainWindow.loadURL(process.env['ELECTRON_RENDERER_URL']);
  } else {
    mainWindow.loadFile(join(__dirname, '../renderer/index.html'));
  }
}

// ─── App lifecycle ────────────────────────────────────────────────────────────

app.whenReady().then(async () => {
  electronApp.setAppUserModelId('com.somewearlabs.beam-ops');
  setupRpcHandlers();
  setupBeamHandlers();
  setupEdgeComputeHandlers();

  app.on('browser-window-created', (_, window) => {
    optimizer.watchWindowShortcuts(window);
  });

  // Start tile server before showing window
  try {
    await startTileServer(MBTILES_PATH);
  } catch (err) {
    console.warn('[Main] Tile server failed to start:', err);
  }

  createWindow();

  app.on('activate', function () {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('window-all-closed', () => {
  stopRpc();
  stopBeamHandlers();
  stopEdgeCompute();
  stopTileServer();
  closeLayerStore();
  if (process.platform !== 'darwin') {
    app.quit();
  }
});
