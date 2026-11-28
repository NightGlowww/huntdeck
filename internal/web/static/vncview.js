// Settings' Browser tab: the login browser's screen, live, through noVNC's
// client (served at /novnc/), and the platforms' tab buttons, which act
// without reloading the page (and so without dropping the screen).
import RFB from '/novnc/core/rfb.js';

const box = document.querySelector('[data-vnc]');
const msg = box && box.querySelector('.vnc-msg');
let rfb = null;
let connected = false;
let connecting = false;
let reconnectTimer = null;

function txt(key, fallback) {
  return (box && box.dataset[key]) || fallback;
}

function running() {
  return !!(box && box.hasAttribute('data-running'));
}

// say shows what the screen can't: connecting, or the browser closed.
function say(reconnecting = false) {
  if (!box || !msg) return;
  let text = '';
  if (!running()) {
    text = txt('msgClosed', 'The browser is closed. Pick a platform above to open it and watch live.');
  } else if (!connected) {
    text = reconnecting
      ? txt('msgReconnecting', 'Reconnecting to the live screen…')
      : txt('msgConnecting', 'Connecting to the live browser screen…');
  }
  msg.textContent = text;
  msg.hidden = text === '';
}

function disconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  connected = false;
  connecting = false;
  if (rfb) {
    const prev = rfb;
    rfb = null;
    try { prev.disconnect(); } catch {}
  }
  say(false);
}

function connect(reconnecting = false) {
  if (!box || !running() || rfb || connecting) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  connecting = true;
  say(reconnecting);
  const url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + box.dataset.vnc;
  const client = new RFB(box.querySelector('.vnc-screen'), url, { shared: true });
  rfb = client;
  client.scaleViewport = true; // the whole screen, fitted to the page
  client.background = 'transparent';
  client.addEventListener('connect', () => {
    if (rfb !== client) return;
    connecting = false;
    connected = true;
    say(false);
  });
  client.addEventListener('disconnect', () => {
    if (rfb !== client) return;
    rfb = null;
    connecting = false;
    connected = false;
    say(true);
    if (running()) {
      reconnectTimer = setTimeout(() => connect(true), 2000);
    }
  });
}

async function pollStatus() {
  if (!box || !box.dataset.statusUrl || document.hidden) return;
  try {
    const res = await fetch(box.dataset.statusUrl, { headers: { Accept: 'application/json' } });
    if (!res.ok) return;
    const d = await res.json();
    box.toggleAttribute('data-running', !!d.running);
    if (d.running && !rfb && !connecting) {
      connect(false);
    } else if (!d.running && (rfb || connecting)) {
      disconnect();
    } else {
      say(false);
    }
  } catch {}
}

if (box) {
  say(false);
  if (running()) {
    connect(false);
  }
  if (box.dataset.statusUrl) {
    setInterval(pollStatus, 4000);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) pollStatus();
    });
  }
}

const errLine = document.querySelector('.bwr-err');
function showError(text) {
  if (!errLine) return;
  errLine.textContent = text;
  errLine.hidden = text === '';
}

// The buttons post as forms without a script; with it, they stay on the page.
for (const form of document.querySelectorAll('form[data-browser]')) {
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const button = e.submitter || form.querySelector('button');
    button.disabled = true;
    try {
      const res = await fetch(form.action, { method: 'POST', body: new FormData(form), headers: { Accept: 'application/json' } });
      const reply = await res.json();
      if (box) {
        box.toggleAttribute('data-running', !!reply.running);
        if (reply.running) {
          connect(false);
        } else {
          disconnect();
        }
      }
      showError(reply.error || '');
    } catch {
      form.submit();
    } finally {
      button.disabled = false;
    }
  });
}
