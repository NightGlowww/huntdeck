#!/usr/bin/env node
// Capture the pages a change touched, in both themes, so the eye can check
// what a test can't: that nothing looks broken.
//
//   HD_PASSWORD='…' node scripts/ui-audit.mjs --base http://127.0.0.1:8080 --out /tmp/ui
//
// Flags:
//   --base URL       where HuntDeck is (default http://127.0.0.1:8080)
//   --out DIR        where the PNGs go (default ./ui-audit)
//   --theme light|dark|both   (default both)
//   --pages 'name=path,…'     (default a spread of the UI)
//   --chrome PATH    the browser (default Chrome on macOS, chromium elsewhere)
//   --sizes W,…      viewport widths to capture, e.g. 390,1280,1400 (default:
//                    the --width, one size)
//   --width N        viewport width when --sizes is not given (default 1400)
//   --password-env NAME       the variable holding the access password
//                             (default HD_PASSWORD; skip it when no password is set)
//
// It logs in once, then loads each page for each theme and saves a PNG. The
// password is only ever read from the environment: it is not printed, not
// written to disk, and not passed on any command line.
import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { existsSync } from 'node:fs';

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf('--' + name);
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};
const base = flag('base', 'http://127.0.0.1:8080').replace(/\/$/, '');
const out = flag('out', 'ui-audit');
const themeArg = flag('theme', 'both');
const sizes = (flag('sizes', '') || String(flag('width', '1400')))
  .split(',').map((s) => Number(s.trim())).filter((n) => n > 0);
if (sizes.length === 0) { console.error('no viewport size'); process.exit(2); }
const passwordEnv = flag('password-env', 'HD_PASSWORD');
const password = process.env[passwordEnv] || '';
const chromePath = flag('chrome',
  existsSync('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
    : 'chromium');
const defaultPages = [
  ['dashboard', '/'],
  ['programs', '/programs'],
  ['targets', '/targets'],
  ['disclosed', '/disclosed'],
  ['settings', '/settings'],
];
const pages = flag('pages', '')
  ? flag('pages', '').split(',').map((p) => p.split('=')).map(([name, path]) => [name.trim(), path.trim()])
  : defaultPages;
const themes = themeArg === 'both' ? ['light', 'dark'] : [themeArg];

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = 9500 + Math.floor(Math.random() * 400);
const profile = join(out, '.chrome-' + port);
await mkdir(out, { recursive: true });
const chrome = spawn(chromePath, ['--headless=new', '--remote-debugging-port=' + port,
  '--user-data-dir=' + profile, '--hide-scrollbars', '--disable-gpu', 'about:blank'], { stdio: 'ignore' });

let version;
for (let i = 0; i < 100; i++) {
  try {
    version = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json();
    break;
  } catch { await sleep(150); }
}
if (!version) { console.error('the browser did not start: ' + chromePath); process.exit(1); }

const ws = new WebSocket(version.webSocketDebuggerUrl);
let id = 0;
const pending = new Map();
ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
};
await new Promise((r) => (ws.onopen = r));
const send = (method, params = {}, sessionId) => {
  const i = ++id;
  ws.send(JSON.stringify({ id: i, method, params, ...(sessionId ? { sessionId } : {}) }));
  return new Promise((r) => pending.set(i, r));
};

const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' });
const { result: { sessionId: S } } = await send('Target.attachToTarget', { targetId, flatten: true });
await send('Page.enable', {}, S);
await send('Runtime.enable', {}, S);

const evaluate = (expression) => send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, S)
  .then((r) => r.result?.result?.value);

await send('Page.navigate', { url: base + '/login' }, S);
await sleep(1500);
if (password) {
  const body = 'password=' + encodeURIComponent(password) + '&next=%2F';
  const status = await evaluate('fetch(' + JSON.stringify(base + '/login') + ', {method:"POST", credentials:"same-origin",'
    + ' headers:{"Content-Type":"application/x-www-form-urlencoded"}, body:' + JSON.stringify(body) + '}).then(r => r.status)');
  if (status !== 200 && status !== 303) { console.error('login answered ' + status); process.exit(1); }
  console.log('signed in');
} else {
  console.log('no ' + passwordEnv + ' set: capturing the pages as they are');
}

for (const width of sizes) {
  const phone = width < 600;
  await send('Emulation.setDeviceMetricsOverride',
    { width, height: phone ? 844 : 900, deviceScaleFactor: 2, mobile: phone }, S);
  await send('Emulation.setTouchEmulationEnabled', { enabled: phone, maxTouchPoints: 5 }, S);
  for (const theme of themes) {
    await send('Page.navigate', { url: base + '/settings' }, S);
    await sleep(1000);
    await evaluate("localStorage.setItem('huntdeck-theme','" + theme + "')");
    for (const [name, path] of pages) {
      await send('Page.navigate', { url: base + path }, S);
      await sleep(1600);
      const shot = await send('Page.captureScreenshot', { format: 'png' }, S);
      const label = sizes.length > 1 ? name + '-' + theme + '-' + width : name + '-' + theme;
      await writeFile(join(out, label + '.png'), Buffer.from(shot.result.data, 'base64'));
      const title = await evaluate('document.title');
      console.log(label.padEnd(34) + (title || '').slice(0, 50));
    }
  }
}

ws.close();
chrome.kill();
console.log('done: ' + out);
