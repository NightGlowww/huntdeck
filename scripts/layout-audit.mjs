#!/usr/bin/env node
// Find boxes that sit side by side but do not share a line: the class of bug
// where two chips in one row are a few pixels apart vertically because one is an
// inline-block and the other a flex item. Nothing in the CSS is "wrong" — the two
// computed styles can be identical — so only geometry can find it.
//
//   HD_PASSWORD='…' node scripts/layout-audit.mjs --base URL [--sizes 390,1400]
//
// Prints one line per suspect row: page, width, parent selector, the children and
// how far apart their tops are. Exit 0 either way; this is a report, not a gate.
import { spawn } from 'node:child_process';
const flag = (name, dflt) => {
  const i = process.argv.indexOf('--' + name);
  return i > 0 && process.argv[i + 1] ? process.argv[i + 1] : dflt;
};
const base = flag('base', 'http://127.0.0.1:8080').replace(/\/$/, '');
const sizes = flag('sizes', '390,1400').split(',').map(Number);
const tolerance = Number(flag('tolerance', '2'));
const password = process.env[flag('password-env', 'HD_PASSWORD')] || '';
const pages = flag('pages',
  'board=/,queue=/queue,programs=/programs,program=/programs/bugcrowd/bitpanda-og-bb,' +
  'settings=/settings,stats=/stats,changes=/changes,disclosed=/disclosed,api=/api')
  .split(',').map(p => { const [n, path] = p.split('='); return [n, path]; });

const chromePath = flag('chrome', process.platform === 'darwin'
  ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : 'chromium');
const port = 9500 + Math.floor(Math.random() * 400);
const sleep = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const chrome = spawn(chromePath, ['--headless=new', '--remote-debugging-port=' + port,
    '--user-data-dir=/tmp/layout-audit-' + port, '--hide-scrollbars', '--disable-gpu', 'about:blank'], { stdio: 'ignore' });
  let ver;
  for (let i = 0; i < 100; i++) {
    try { ver = await (await fetch('http://127.0.0.1:' + port + '/json/version')).json(); break; } catch { await sleep(150); }
  }
  const ws = new WebSocket(ver.webSocketDebuggerUrl);
  let id = 0; const pending = new Map();
  ws.onmessage = e => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
  await new Promise(r => (ws.onopen = r));
  const send = (method, params = {}, sid) => {
    const i = ++id;
    ws.send(JSON.stringify({ id: i, method, params, ...(sid ? { sessionId: sid } : {}) }));
    return new Promise(r => pending.set(i, r));
  };
  const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' });
  const { result: { sessionId: S } } = await send('Target.attachToTarget', { targetId, flatten: true });
  await send('Page.enable', {}, S); await send('Runtime.enable', {}, S);
  const ev = expr => send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true }, S)
    .then(r => r.result?.result?.value);

  if (password) {
    await send('Page.navigate', { url: base + '/login' }, S); await sleep(1500);
    await ev('fetch("/login",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/x-www-form-urlencoded"},body:' +
      JSON.stringify('password=' + encodeURIComponent(password) + '&next=%2F') + '})');
    await sleep(400);
  }

  const probe = String.raw`(() => {
    const tol = TOL;
    const out = [];
    const sel = el => el.tagName.toLowerCase() + (el.className && typeof el.className === 'string'
      ? '.' + el.className.trim().split(/\s+/).join('.') : '');
    for (const parent of document.querySelectorAll('body *')) {
      const kids = [...parent.children].filter(k => {
        const r = k.getBoundingClientRect();
        return r.width > 0 && r.height > 0 && getComputedStyle(k).visibility !== 'hidden';
      });
      if (kids.length < 2) continue;
      for (let i = 0; i < kids.length; i++) {
        for (let j = i + 1; j < kids.length; j++) {
          const a = kids[i].getBoundingClientRect(), b = kids[j].getBoundingClientRect();
          const sideBySide = a.right <= b.left + 1 || b.right <= a.left + 1;
          if (!sideBySide) continue;            // stacked: different tops are expected
          const d = Math.abs(a.top - b.top);
          if (d <= tol || d > 12) continue;     // aligned, or clearly a different row
          // Same height and side by side means one line: a differing top is the bug.
          // Different heights are usually a label above a field, or an icon.
          if (Math.abs(a.height - b.height) > 1) continue;
          out.push({ parent: sel(parent), a: sel(kids[i]), b: sel(kids[j]), delta: +d.toFixed(1) });
        }
      }
    }
    return out.slice(0, 20);
  })()`;

  let total = 0;
  for (const width of sizes) {
    await send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: false }, S);
    for (const [name, path] of pages) {
      await send('Page.navigate', { url: base + path }, S); await sleep(1400);
      const found = await ev(probe.replace('TOL', String(tolerance)));
      for (const f of found || []) {
        total++;
        console.log(`${name}@${width}  ${f.parent}  ${f.a} vs ${f.b}  ${f.delta}px`);
      }
    }
  }
  console.log(total === 0 ? 'no misaligned rows found' : `${total} row(s) to look at`);
  ws.close(); chrome.kill();
})();
