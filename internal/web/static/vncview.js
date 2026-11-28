// Settings' Browser tab: opt-in live browser screen via noVNC (/novnc/), plus
// the platforms' tab buttons which act without reloading the page.
import RFB from '/novnc/core/rfb.js';

const wrap = document.querySelector('[data-vnc-wrap]');
const box = document.querySelector('[data-vnc]');
const msg = box && box.querySelector('.vnc-msg');
const msgText = box && box.querySelector('.vnc-msg-text');
const retryBtn = box && box.querySelector('[data-vnc-retry]');
const toggleBtn = document.querySelector('[data-vnc-toggle]');
const stateChip = document.querySelector('[data-browser-state]');

let watching = false;
let rfb = null;
let connected = false;
let connecting = false;
let everConnected = false;
let attempts = 0;
let failed = false;
let reconnectTimer = null;
let statusTimer = null;

function txt(key, fallback) {
  return (box && box.dataset[key]) || fallback;
}

function running() {
  return !!(box && box.hasAttribute('data-running'));
}

function setRunningState(isRunning) {
  if (box) {
    box.toggleAttribute('data-running', !!isRunning);
  }
  if (stateChip) {
    stateChip.classList.toggle('ok', !!isRunning);
    stateChip.textContent = isRunning
      ? (stateChip.dataset.labelRunning || 'Running')
      : (stateChip.dataset.labelStopped || 'Stopped');
  }
}

// say shows what the screen can't: connecting, failure with retry, or browser closed.
function say(reconnecting = false) {
  if (!box || !msg || !msgText) return;
  let text = '';
  if (!running()) {
    text = txt('msgClosed', 'The browser is closed. Pick a platform above to open it and watch live.');
  } else if (failed) {
    text = txt('msgFailed', 'Could not connect to the live browser screen. Check Logs or try again.');
  } else if (!connected) {
    text = reconnecting
      ? txt('msgReconnecting', 'Reconnecting to the live screen…')
      : txt('msgConnecting', 'Connecting to the live browser screen…');
  }
  msgText.textContent = text;
  if (retryBtn) {
    retryBtn.hidden = !(failed && running());
  }
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
  if (!watching || !box || !running() || rfb || connecting) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  connecting = true;
  failed = false;
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
    everConnected = true;
    attempts = 0;
    failed = false;
    say(false);
  });
  client.addEventListener('disconnect', () => {
    if (rfb !== client) return;
    rfb = null;
    connecting = false;
    connected = false;
    if (!watching || !running()) {
      say(false);
      return;
    }
    attempts += 1;
    if (!everConnected || attempts >= 3) {
      failed = true;
      say(false);
      return;
    }
    say(true);
    reconnectTimer = setTimeout(() => connect(true), 1500);
  });
}

async function pollStatus() {
  if (!watching || !box || !box.dataset.statusUrl || document.hidden) return;
  try {
    const res = await fetch(box.dataset.statusUrl, { headers: { Accept: 'application/json' } });
    if (!res.ok) return;
    const d = await res.json();
    setRunningState(!!d.running);
    if (d.running && !rfb && !connecting && !failed) {
      connect(false);
    } else if (!d.running && (rfb || connecting || failed)) {
      failed = false;
      attempts = 0;
      disconnect();
    } else {
      say(false);
    }
  } catch {}
}

function startPolling() {
  if (statusTimer || !box || !box.dataset.statusUrl) return;
  statusTimer = setInterval(pollStatus, 4000);
}

function stopPolling() {
  if (!statusTimer) return;
  clearInterval(statusTimer);
  statusTimer = null;
}

function setWatching(on) {
  watching = !!on;
  if (wrap) {
    wrap.hidden = !watching;
  }
  if (toggleBtn) {
    toggleBtn.setAttribute('aria-expanded', watching ? 'true' : 'false');
    toggleBtn.textContent = watching
      ? (toggleBtn.dataset.labelClose || 'Hide screen')
      : (toggleBtn.dataset.labelOpen || 'Watch screen');
  }
  if (!watching) {
    stopPolling();
    failed = false;
    attempts = 0;
    disconnect();
    return;
  }
  attempts = 0;
  everConnected = false;
  failed = false;
  say(false);
  startPolling();
  pollStatus();
  if (running()) {
    connect(false);
  }
}

if (toggleBtn) {
  toggleBtn.addEventListener('click', () => setWatching(!watching));
}

if (retryBtn) {
  retryBtn.addEventListener('click', () => {
    attempts = 0;
    failed = false;
    connect(false);
  });
}

document.addEventListener('visibilitychange', () => {
  if (watching && !document.hidden) pollStatus();
});

window.addEventListener('pagehide', () => {
  if (watching) {
    stopPolling();
    disconnect();
  }
});

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
      setRunningState(!!reply.running);
      if (reply.running) {
        if (watching && !rfb && !connecting) {
          attempts = 0;
          failed = false;
          connect(false);
        }
      } else {
        failed = false;
        attempts = 0;
        if (watching || rfb || connecting) {
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
