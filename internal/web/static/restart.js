// Settings' Accounts tab, while a restart waits to apply saved settings:
// asks every few seconds whether it's still waiting, and reloads the page
// once the restarted server says it isn't (requests fail while it restarts).
(() => {
  const init = (_root, signal) => {
    const bar = document.querySelector("[data-restart]");
    if (!bar) return;
    let timer = 0;

    async function check() {
      if (signal.aborted) return;
      try {
        const res = await fetch("/settings/restart", { cache: "no-store", headers: { Accept: "application/json" }, signal });
        if (res.ok && !(await res.json()).pending) {
          const tab = new URLSearchParams(location.search).get("tab") || "accounts";
          location.replace(location.pathname + "?tab=" + encodeURIComponent(tab) + location.hash);
          return;
        }
      } catch {
        // restarting: try again
      }
      if (!signal.aborted) timer = setTimeout(check, 3000);
    }

    timer = setTimeout(check, 2000);
    signal.addEventListener("abort", () => clearTimeout(timer), { once: true });
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init(document, new AbortController().signal);
})();
