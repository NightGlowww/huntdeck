#!/usr/bin/env node
// Ask the running page a question and print its answer, so a claim about the
// interface ("the header stays put while the table scrolls") can be checked
// against the browser instead of guessed from a screenshot.
//
//   HD_PASSWORD='…' node scripts/dom-probe.mjs --base URL --path /queue \
//     --js 'document.querySelectorAll("thead th").length'
//
// Flags:
//   --base URL       where HuntDeck is (default http://127.0.0.1:8080)
//   --path PATH      the page to open (default /)
//   --js EXPRESSION  evaluated in the page after load and a scroll, printed as JSON
//   --scroll N       scroll the window N pixels before evaluating (default 0)
//   --width N        viewport width (default 1400)
//   --chrome PATH    the browser (default Chrome on macOS, chromium elsewhere)
//   --password-env   the variable holding the access password (default HD_PASSWORD)
//
// The password is read from the environment only: never printed, never written
// to disk, never passed on a command line.

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';

const args = process.argv.slice(2);
const flag = (name, dflt) => {
  const i = args.indexOf('--' + name);
  return i >= 0 ? args[i + 1] : dflt;
};
const base = flag('base', 'http://127.0.0.1:8080').replace(/\/$/, '');
const path = flag('path', '/');
const js = flag('js', 'document.title');
const scroll = Number(flag('scroll', '0'));
const width = Number(flag('width', '1400'));
const passwordEnv = flag('password-env', 'HD_PASSWORD');
const password = process.env[passwordEnv] || '';

const chromePath = flag('chrome', process.platform === 'darwin'
  ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
  : 'chromium');
if (!existsSync(chromePath) && chromePath !== 'chromium') {
  console.error('no browser at ' + chromePath);
  process.exit(2);
}

const port = 9300 + Math.floor(Math.random() * 400);
const chrome = spawn(chromePath, [
  '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
  '--remote-debugging-port=' + port, '--user-data-dir=/tmp/dom-probe-' + port,
  '--window-size=' + width + ',900', 'about:blank',
], { stdio: 'ignore' });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function wsURL() {
  for (let i = 0; i < 60; i++) {
    try {
      const res = await fetch('http://127.0.0.1:' + port + '/json/version');
      const j = await res.json();
      if (j.webSocketDebuggerUrl) return j.webSocketDebuggerUrl;
    } catch { /* not up yet */ }
    await sleep(150);
  }
  throw new Error('the browser never came up');
}

const url = await wsURL();
const ws = new WebSocket(url);
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
  .then((r) => {
    if (r.result?.exceptionDetails) {
      throw new Error(r.result.exceptionDetails.text + ': ' + (r.result.exceptionDetails.exception?.description || ''));
    }
    return r.result?.result?.value;
  });

await send('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false }, S);
await send('Page.navigate', { url: base + '/login' }, S);
await sleep(1200);
if (password) {
  const body = 'password=' + encodeURIComponent(password) + '&next=%2F';
  const status = await evaluate('fetch(' + JSON.stringify(base + '/login') + ', {method:"POST", credentials:"same-origin", headers:{"Content-Type":"application/x-www-form-urlencoded"}, body:' + JSON.stringify(body) + '}).then(r => r.status)');
  if (status !== 200 && status !== 303) { console.error('login answered ' + status); process.exit(1); }
}
await send('Page.navigate', { url: base + path }, S);
await sleep(1200);
if (scroll) {
  await evaluate('window.scrollTo(0, ' + scroll + '); document.documentElement.scrollTop');
  await sleep(250);
}
console.log(JSON.stringify(await evaluate(js), null, 2));
chrome.kill();
ws.close();
