/**
 * Playwright driver for beam-ops Electron app (macOS).
 * Usage: node scripts/driver.mjs
 * Commands: launch, ss [name], click <sel>, eval <expr>, text [sel], windows, quit, help
 */
import { _electron as electron } from 'playwright-core';
import * as readline from 'node:readline';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const APP_DIR = path.resolve(__dirname, '..');
const SHOT_DIR = process.env.SCREENSHOT_DIR || '/tmp/beam-ops-shots';
fs.mkdirSync(SHOT_DIR, { recursive: true });

const ELECTRON_BIN = path.join(
  APP_DIR,
  'node_modules/electron/dist/Electron.app/Contents/MacOS/Electron'
);

let app = null;
let page = null;
let devProc = null;

const COMMANDS = {
  async launch() {
    if (app) return console.log('already launched');

    // Start electron-vite dev server + Electron together
    console.log('Starting electron-vite dev...');
    devProc = spawn('npm', ['run', 'dev'], {
      cwd: APP_DIR,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env }
    });

    // Wait for the vite renderer dev server to be ready
    await new Promise((resolve) => {
      devProc.stdout.on('data', (d) => {
        const s = d.toString();
        process.stdout.write('[dev] ' + s);
        if (s.includes('start electron app')) resolve();
      });
      devProc.stderr.on('data', (d) => process.stderr.write('[dev-err] ' + d));
      setTimeout(resolve, 15000); // fallback
    });

    console.log('Attaching Playwright...');
    // Give Electron a moment to open its window
    await new Promise(r => setTimeout(r, 5000));

    app = await electron.launch({
      executablePath: ELECTRON_BIN,
      args: ['.'],
      cwd: APP_DIR,
      env: { ...process.env, ELECTRON_RENDERER_URL: 'http://localhost:5173/' },
      timeout: 30000,
    });

    await new Promise(r => setTimeout(r, 6000));

    const windows = app.windows();
    page = windows.find(w => !w.url().startsWith('devtools://')) ?? await app.firstWindow();
    console.log(`launched. ${windows.length} window(s):`);
    for (const w of windows) console.log(' ', w.url());
  },

  async ss(name) {
    if (!page) return console.log('ERROR: launch first');
    const f = path.join(SHOT_DIR, (name || `ss-${Date.now()}`) + '.png');
    await page.screenshot({ path: f, fullPage: false });
    console.log('screenshot:', f);
  },

  async click(sel) {
    if (!page) return console.log('ERROR: launch first');
    const r = await page.evaluate(s => {
      const el = document.querySelector(s);
      if (!el) return 'NOT_FOUND';
      el.click(); return 'OK';
    }, sel);
    console.log('click', sel, '→', r);
  },

  async 'click-text'(text) {
    if (!page) return console.log('ERROR: launch first');
    const r = await page.evaluate(t => {
      const els = [...document.querySelectorAll('button, a, [role="button"], div[onClick]')];
      const el = els.find(e => e.textContent?.trim() === t)
              ?? els.find(e => e.textContent?.includes(t));
      if (!el) return 'NOT_FOUND';
      el.click(); return 'OK: ' + el.tagName;
    }, text);
    console.log('click-text', JSON.stringify(text), '→', r);
  },

  async wait(sel) {
    if (!page) return console.log('ERROR: launch first');
    try {
      await page.waitForSelector(sel, { timeout: 15000 });
      console.log('found:', sel);
    } catch {
      console.log('TIMEOUT waiting for:', sel);
    }
  },

  async eval(expr) {
    if (!page) return console.log('ERROR: launch first');
    try { console.log(JSON.stringify(await page.evaluate(expr))); }
    catch (e) { console.log('ERROR:', e.message); }
  },

  async text(sel) {
    if (!page) return console.log('ERROR: launch first');
    console.log(await page.evaluate(
      s => (s ? document.querySelector(s) : document.body)?.innerText?.slice(0, 800) ?? '(null)',
      sel || null
    ));
  },

  async windows() {
    if (!app) return console.log('ERROR: launch first');
    for (const w of app.windows()) console.log(' page:', w.url());
  },

  async quit() {
    if (devProc) { devProc.kill('SIGTERM'); devProc = null; }
    if (app) { await app.close().catch(() => {}); app = null; page = null; }
    console.log('quit');
  },

  help() {
    console.log('commands: launch, ss [name], click <sel>, click-text <text>, wait <sel>, eval <expr>, text [sel], windows, quit');
  },
};

const stdin = fs.createReadStream(null, { fd: fs.openSync('/dev/stdin', 'r') });
const rl = readline.createInterface({ input: stdin, output: process.stdout, prompt: 'driver> ' });

rl.on('line', async line => {
  const [cmd, ...rest] = line.trim().split(/\s+/);
  if (!cmd) return rl.prompt();
  const fn = COMMANDS[cmd];
  if (!fn) { console.log('unknown:', cmd, '— try: help'); return rl.prompt(); }
  try { await fn(rest.join(' ')); } catch (e) { console.log('ERROR:', e.message); }
  if (cmd === 'quit') { rl.close(); process.exit(0); }
  rl.prompt();
});

rl.on('close', async () => { await COMMANDS.quit(); process.exit(0); });
console.log('beam-ops driver ready — type "launch" to start, "help" for commands');
rl.prompt();
