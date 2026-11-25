#!/usr/bin/env node
// Find English left on a page in another language: the visual complement to
// `make i18n` (templates) and the Go-label tests (titles, keys, fields).
//
//   HD_PASSWORD=… node scripts/i18n-audit.mjs --base URL --lang zh \
//     --pages 'name=/path,…'
//
// It reads the rendered text - not the template - so it sees what a reader
// sees: a handler's sentence, a plural() key printed raw, a fragment glued to
// a platform name. Brands, code, hosts and file names are not English copy.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { existsSync } from 'node:fs';

const args = process.argv.slice(2);
const flag = (n, d) => { const i = args.indexOf('--' + n); return i >= 0 && args[i + 1] ? args[i + 1] : d; };
const base = flag('base', 'http://127.0.0.1:8080').replace(/\/$/, '');
const lang = flag('lang', 'zh');
const passwordEnv = flag('password-env', 'HD_PASSWORD');
const password = process.env[passwordEnv] || '';
const out = flag('out', '/tmp/i18n-audit');
const pages = flag('pages', '').split(',').filter(Boolean).map((p) => {
  const i = p.indexOf('=');
  return [p.slice(0, i).trim(), p.slice(i + 1).trim()];
});
const chromePath = flag('chrome', existsSync('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
  ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : 'chromium');

// Brands, protocols and file names a page may show as they are.
const ALLOW = new Set((
  'huntdeck huntmail hackerone bugcrowd intigriti yeswehack pojia scopesentry huntscore ' +
  'api url urls ip cidr json csv http https get post put delete cve cvss cwe bbp vdp id ids ' +
  'caido burp zap discord telegram webhook burpsuite scope insomnia bruno postman chrome github ' +
  'signal tls ssl vnc env sqlite cloudflare workers d1 openai typesafe bailian jev ssk ssv ' +
  'dkim dmarc ok on off user agent token key cookie cookies'
).split(/\s+/));

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = 9900 + Math.floor(Math.random() * 90);
const profile = join(out, '.chrome-' + port);
await mkdir(out, { recursive: true });
const chrome = spawn(chromePath, ['--headless=new', '--remote-debugging-port=' + port,
  '--user-data-dir=' + profile, '--hide-scrollbars', '--disable-gpu', 'about:blank'], { stdio: 'ignore' });

let version;
for (let i = 0; i < 100; i++) {
  try { version = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json(); break; } catch { await sleep(150); }
}
if (!version) { console.error('the browser did not start'); process.exit(1); }

const ws = new WebSocket(version.webSocketDebuggerUrl);
let id = 0;
const pending = new Map();
ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
await new Promise((r) => (ws.onopen = r));
const send = (method, params = {}, sid) => { const i = ++id; ws.send(JSON.stringify({ id: i, method, params, ...(sid ? { sessionId: sid } : {}) })); return new Promise((r) => pending.set(i, r)); };
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' });
const { result: { sessionId: S } } = await send('Target.attachToTarget', { targetId, flatten: true });
await send('Page.enable', {}, S);
await send('Runtime.enable', {}, S);
const evaluate = (expression) => send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, S)
  .then((r) => r.result?.result?.value);

await send('Page.navigate', { url: base + '/login' }, S);
await sleep(900);
if (password) {
  const body = 'password=' + encodeURIComponent(password) + '&next=%2F';
  await evaluate('fetch(' + JSON.stringify(base + '/login') + ', {method:"POST", credentials:"same-origin",' +
    ' headers:{"Content-Type":"application/x-www-form-urlencoded"}, body:' + JSON.stringify(body) + '})');
}
await evaluate('document.cookie = "hd_lang=' + lang + ';path=/;max-age=31536000"');

// Collect the text a reader sees, skipping code, pre and form values.
const COLLECT = `(() => {
  const out = [];
  const walk = (n) => {
    if (n.nodeType === 3) {
      const t = (n.nodeValue || '').replace(/\\s+/g, ' ').trim();
      if (t) {
        const p = n.parentElement;
        const tag = p ? p.tagName.toLowerCase() : '';
        if (!['code', 'pre', 'script', 'style', 'textarea'].includes(tag)) out.push(t);
      }
      return;
    }
    if (n.nodeType !== 1) return;
    const el = n;
    const tag = el.tagName.toLowerCase();
    if (['script', 'style', 'code', 'pre', 'textarea', 'svg'].includes(tag)) return;
    for (const c of el.childNodes) walk(c);
  };
  walk(document.body);
  return out;
})()`;

let total = 0;
for (const [name, path] of pages) {
  await send('Page.navigate', { url: base + path }, S);
  await sleep(1200);
  const texts = await evaluate(COLLECT) || [];
  const hits = [];
  for (const t of texts) {
    // A token carrying . / _ : @ or a digit is an identifier - a host, a path,
    // a file name, a CVE - not copy. Words left are the candidate prose.
    const clean = t.split(/\s+/).filter((w) => !/[.\/_:@\d]/.test(w)).join(' ');
    const words = (clean.match(/[A-Za-z][A-Za-z'’-]*/g) || []);
    const prose = words.filter((w) => !ALLOW.has(w.toLowerCase()));
    if (words.length >= 2 && prose.length >= 2) hits.push(t.slice(0, 120));
  }
  if (hits.length) {
    console.log('== ' + name + ' (' + hits.length + ')');
    for (const h of hits.slice(0, 12)) console.log('   ' + h);
    total += hits.length;
  }
}
console.log('english fragments: ' + total);
ws.close();
chrome.kill();
process.exit(0);
