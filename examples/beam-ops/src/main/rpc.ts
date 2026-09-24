import { ipcMain } from 'electron';
import { spawn, execSync, ChildProcess } from 'child_process';
import * as os from 'os';
import * as path from 'path';

function getRpcBin(): string {
  // Matches the production install location from install-rpc.sh (downloaded from GCS).
  // Dev deploy also installs here via `make ship-local`.
  return path.join(os.homedir(), 'bin', 'rpc');
}

// Kill any leftover rpc binary from a previous session that may still be
// holding the webhook port (:8080).
function killZombies(): void {
  try {
    execSync('pkill -9 -f "rpc_"', { stdio: 'ignore' });
  } catch {
    // Nothing matched — pkill exits 1 when no processes were found.
  }
}

const ANSI_RE = /\x1B\[[0-9;]*[mGKHFJA-Za-z]|\x1B[()][AB01]/g;

function processOutput(raw: string): string[] {
  const out: string[] = [];
  for (const line of raw.replace(ANSI_RE, '').split('\n')) {
    const last = line.split('\r').pop()!.trimEnd();
    if (/^>\s*$/.test(last)) continue;
    if (/^\s*waiting\.\.\./i.test(last)) continue;
    if (/^\s*\d+\.\d+s\s*$/.test(last)) continue;
    // rpc binary internal log lines  e.g. "2026/08/31 14:49:10 [exec] ..."
    if (/^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2} \[/.test(last)) continue;
    // shell startup banner / hints
    if (/^connecting\.\.\.$/.test(last)) continue;
    if (/^Somewear remote shell/.test(last)) continue;
    if (/^Ctrl-C or 'exit' to quit/.test(last)) continue;
    out.push(last);
  }
  return out;
}

let child: ChildProcess | null = null;
// Monotonically increasing. Incremented in TWO places:
//   1. killChild() — immediately invalidates the killed process's handlers.
//   2. rpc:start — creates a fresh, unique session for the new child.
// This ensures a killed child's close/error events are always stale by the
// time rpc:start #2 awaits pendingDeath and the close event fires.
let currentSession = 0;

// Resolves once the most recently killed child has fully exited (and therefore
// released its port). Always awaited by rpc:start before spawning.
let pendingDeath: Promise<void> = Promise.resolve();

function killChild(): void {
  if (!child) return;
  const dying = child;
  child = null;
  // Increment now so the dying process's close/error handlers are immediately
  // stale — even before its close event fires and pendingDeath resolves.
  currentSession++;
  pendingDeath = new Promise((resolve) => {
    let settled = false;
    const finish = () => { if (!settled) { settled = true; resolve(); } };
    dying.once('close', finish);
    dying.kill('SIGKILL');
    // SIGKILL is instant on POSIX; 800 ms is a pure safety net.
    setTimeout(finish, 800);
  });
}

export function setupRpcHandlers(): void {
  killZombies();

  ipcMain.handle('rpc:start', async (event, workspaceId: number, targetUserId?: number) => {
    killChild();       // Kill any running child; its session is now stale.
    await pendingDeath; // Wait for it to fully exit and release :8080.
    // Create a fresh session for the new child *after* the old one is gone.
    const session = ++currentSession;

    const args = ['shell', '--timeout', '30m', '--webhook-port', '8080', '--workspace', String(workspaceId)];
    if (targetUserId) {
      args.push('--target-user-id', String(targetUserId));
    }

    child = spawn(getRpcBin(), args, { stdio: ['pipe', 'pipe', 'pipe'] });

    child.stdout?.on('data', (buf: Buffer) => {
      if (session !== currentSession || event.sender.isDestroyed()) return;
      for (const line of processOutput(buf.toString())) {
        event.sender.send('rpc:output', line);
      }
    });

    child.stderr?.on('data', (buf: Buffer) => {
      if (session !== currentSession || event.sender.isDestroyed()) return;
      for (const line of processOutput(buf.toString())) {
        event.sender.send('rpc:output', line);
      }
    });

    child.on('close', (code) => {
      if (session !== currentSession) return;
      child = null;
      if (event.sender.isDestroyed()) return;
      event.sender.send('rpc:exit', code);
    });

    child.on('error', (err) => {
      if (session !== currentSession) return;
      child = null;
      if (event.sender.isDestroyed()) return;
      event.sender.send('rpc:error', err.message);
    });
  });

  ipcMain.handle('rpc:send', (_, command: string) => {
    if (!child) throw new Error('shell not running: no process');
    if (!child.stdin?.writable) throw new Error('shell not running: stdin closed');
    return new Promise<void>((resolve, reject) => {
      child!.stdin!.write(command + '\n', (err) => {
        if (err) reject(new Error(`write failed: ${err.message}`));
        else resolve();
      });
    });
  });

  ipcMain.handle('rpc:stop', () => { killChild(); });
}

export function stopRpc(): void {
  child?.kill('SIGKILL');
  child = null;
}
