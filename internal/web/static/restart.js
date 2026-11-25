// Settings' Accounts tab, while a restart waits to apply saved settings:
// asks every few seconds whether it's still waiting, and reloads the page
// once the restarted server says it isn't (requests fail while it restarts).
const bar = document.querySelector('[data-restart]');

async function check() {
  try {
    const res = await fetch('/settings/restart', { cache: 'no-store', headers: { Accept: 'application/json' } });
    if (res.ok && !(await res.json()).pending) {
      const tab = new URLSearchParams(location.search).get('tab') || 'accounts';
      location.replace(location.pathname + '?tab=' + encodeURIComponent(tab) + location.hash);
      return;
    }
  } catch {
    // restarting: try again
  }
  setTimeout(check, 3000);
}

if (bar) setTimeout(check, 2000);
