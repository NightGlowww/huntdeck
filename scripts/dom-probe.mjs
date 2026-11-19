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
//   --focus          instead of --js: check every focusable control shows a focus
//                    ring when focused, and exit 1 when one does not
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
const cookieFile = flag('cookie', '');

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
  // Fifteen seconds, twice as long as a healthy start, because this probe is
  // evidence rather than convenience: a launch that times out must say so.
  const deadline = Date.now() + 15000;
  let lastErr = null;
  while (Date.now() < deadline) {
    try {
      const res = await fetch('http://127.0.0.1:' + port + '/json/version');
      const j = await res.json();
      if (j.webSocketDebuggerUrl) return j.webSocketDebuggerUrl;
    } catch (e) { lastErr = e; }
    await sleep(150);
  }
  throw new Error('the browser never came up on port ' + port + (lastErr ? ': ' + lastErr.message : ''));
}

// A failed launch exits with the reason on stderr and a non-zero code, never
// with a quiet empty line: a caller that cannot tell "0" from "did not run"
// will read the empty answer as a pass.
let url;
try {
  url = await wsURL();
} catch (e) {
  console.error('dom-probe: ' + e.message);
  chrome.kill();
  process.exit(3);
}
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
// A session cookie on disk is reused: an audit that logs in on every page
// walks into the login budget and gets 429, which reads as a broken page.
const { readFile, writeFile } = await import('node:fs/promises');
let hadCookie = false;
if (cookieFile) {
  try {
    const c = JSON.parse(await readFile(cookieFile, 'utf8'));
    await send('Network.setCookie', { name: c.name, value: c.value, domain: new URL(base).hostname, path: '/', secure: base.startsWith('https') }, S);
    hadCookie = true;
  } catch { /* no cookie yet */ }
}
if (password && !hadCookie) {
  const body = 'password=' + encodeURIComponent(password) + '&next=%2F';
  const status = await evaluate('fetch(' + JSON.stringify(base + '/login') + ', {method:"POST", credentials:"same-origin", headers:{"Content-Type":"application/x-www-form-urlencoded"}, body:' + JSON.stringify(body) + '}).then(r => r.status)');
  if (status !== 200 && status !== 303) {
    console.error('login answered ' + status + (status === 429 ? ' (the login budget is spent: wait, or pass --cookie)' : ''));
    chrome.kill();
    process.exit(1);
  }
  if (cookieFile) {
    // The session cookie is HttpOnly by design, so document.cookie never
    // shows it: read it from the browser instead, or every later run logs in
    // again and the login budget answers 429.
    const got = await send('Network.getCookies', { urls: [base] }, S);
    const jar = (got.result?.cookies || []).find((c) => c.name === 'hd_session');
    if (jar) await writeFile(cookieFile, JSON.stringify({ name: 'hd_session', value: jar.value }));
  }
}
await send('Page.navigate', { url: base + path }, S);
await sleep(1200);
if (scroll) {
  await evaluate('window.scrollTo(0, ' + scroll + '); document.documentElement.scrollTop');
  await sleep(250);
}
// A disabled control cannot take focus, so its style never changes: counting
// it as missing a ring is an artefact of the check, not a defect.
const FOCUS_CHECK = `(() => {
  const els = [...document.querySelectorAll("a[href], button:not([disabled]), input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])")]
    // A closed <details> keeps its content in a hidden subtree: the element still
    // has computed styles and a layout box, but no user can focus it, so
    // counting it reports a ring that is missing from nothing.
    .filter((el) => !el.disabled && el.offsetParent !== null && !el.closest("details:not([open])"));
  const style = (el) => { const s = getComputedStyle(el); return s.outlineStyle + "|" + s.outlineWidth + "|" + s.boxShadow; };
  const missing = [];
  for (const el of els) {
    el.blur(); const b = style(el); el.focus(); const a = style(el);
    if (a === b) missing.push({ tag: el.tagName, cls: (el.className || '').toString().slice(0, 24), text: (el.textContent || '').trim().slice(0, 24) });
  }
  if (document.activeElement) document.activeElement.blur();
  return { focusable: els.length, withoutVisibleFocus: missing.length, samples: missing.slice(0, 8) };
})()`;
const wantFocus = args.includes('--focus');
const answer = await evaluate(wantFocus ? FOCUS_CHECK : js);
console.log(JSON.stringify(answer, null, 2));
// A page with no focusable controls, or an answer that did not serialise,
// must not read as a pass: the check exists to look at something.
if (wantFocus) {
  if (!answer || typeof answer.focusable !== 'number') {
    console.error('dom-probe: the focus check produced no answer');
    process.exitCode = 3;
  } else if (answer.focusable === 0) {
    console.error('dom-probe: no focusable controls on ' + path + ': nothing was checked');
    process.exitCode = 1;
  } else if (answer.withoutVisibleFocus > 0) {
    process.exitCode = 1;
  }
}
chrome.kill();
ws.close();
