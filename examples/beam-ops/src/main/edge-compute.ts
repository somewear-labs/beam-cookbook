import { ipcMain, app } from 'electron';
import { spawn, ChildProcess } from 'child_process';
import { existsSync } from 'fs';
import * as os from 'os';
import * as path from 'path';
import * as fs from 'fs';

export interface EdgeConfig {
  port: number;
  workspaceId: number;
}

const DEFAULT_CONFIG: EdgeConfig = { port: 8080, workspaceId: 0 };

function getConfigPath(): string {
  return path.join(app.getPath('userData'), 'edge-compute.json');
}

function loadConfig(): EdgeConfig {
  try {
    return { ...DEFAULT_CONFIG, ...JSON.parse(fs.readFileSync(getConfigPath(), 'utf-8')) };
  } catch {
    return { ...DEFAULT_CONFIG };
  }
}

function saveConfig(cfg: EdgeConfig): void {
  fs.writeFileSync(getConfigPath(), JSON.stringify(cfg, null, 2));
}

function getRpcBin(): string {
  const platform = os.platform();
  const arch = os.arch();
  const osPart = platform === 'win32' ? 'windows' : platform;
  const archPart = arch === 'arm64' ? 'arm64' : 'amd64';
  const binName = `rpc_${osPart}_${archPart}`;

  const fromUserData = path.join(app.getPath('userData'), binName);
  if (existsSync(fromUserData)) return fromUserData;

  return app.isPackaged
    ? path.join(path.resolve(app.getAppPath(), '..'), 'beam-cookbook', 'rpc', 'bin', binName)
    : path.join(app.getAppPath(), '..', '..', 'rpc', 'bin', binName);
}

const ANSI_RE = /\x1B\[[0-9;]*[mGKHFJA-Za-z]|\x1B[()][AB01]/g;

let child: ChildProcess | null = null;
let currentSession = 0;
let pendingDeath: Promise<void> = Promise.resolve();

function killChild(): void {
  if (!child) return;
  const dying = child;
  child = null;
  currentSession++;
  pendingDeath = new Promise((resolve) => {
    let settled = false;
    const finish = () => { if (!settled) { settled = true; resolve(); } };
    dying.once('close', finish);
    dying.kill('SIGKILL');
    setTimeout(finish, 800);
  });
}

export function setupEdgeComputeHandlers(): void {
  ipcMain.handle('edge:load-config', () => loadConfig());

  ipcMain.handle('edge:status', () => ({ running: child !== null }));

  ipcMain.handle('edge:start', async (event, port: number, workspaceId: number) => {
    killChild();
    await pendingDeath;
    const session = ++currentSession;

    saveConfig({ port, workspaceId });

    const args = ['server', '--port', String(port), '--workspace', String(workspaceId)];
    child = spawn(getRpcBin(), args, { stdio: ['ignore', 'pipe', 'pipe'] });

    const push = (buf: Buffer) => {
      if (session !== currentSession || event.sender.isDestroyed()) return;
      for (const raw of buf.toString().split('\n')) {
        const line = raw.replace(ANSI_RE, '').trimEnd();
        if (line) event.sender.send('edge:output', line);
      }
    };

    child.stdout?.on('data', push);
    child.stderr?.on('data', push);

    child.on('close', (code) => {
      if (session !== currentSession) return;
      child = null;
      if (!event.sender.isDestroyed()) event.sender.send('edge:exit', code);
    });

    child.on('error', (err) => {
      if (session !== currentSession) return;
      child = null;
      if (!event.sender.isDestroyed()) event.sender.send('edge:error', err.message);
    });
  });

  ipcMain.handle('edge:stop', () => { killChild(); });
}

export function stopEdgeCompute(): void {
  child?.kill('SIGKILL');
  child = null;
}
