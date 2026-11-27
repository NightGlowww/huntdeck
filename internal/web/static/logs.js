// Live auto-refresh polling for System > Logs (/logs).
(() => {
  const init = (_root, signal) => {
    const box = document.querySelector("[data-logs-root]");
    if (!box || box.dataset.logsReady === "1") return;
    box.dataset.logsReady = "1";

    const list = box.querySelector("[data-logs-list]");
    const toggle = box.querySelector("[data-logs-auto]");
    const level = box.dataset.level || "all";
    const query = box.dataset.query || "";
    const emptyText = box.querySelector("[data-logs-empty]")?.textContent || "";

    if (!list || !toggle) return;

    let timer = 0;
    let inFlight = false;

    const buildTailURL = () => {
      const u = new URL("/logs/tail", location.origin);
      if (level && level !== "all") u.searchParams.set("level", level);
      if (query) u.searchParams.set("q", query);
      return u.pathname + u.search;
    };

    const renderEntries = (entries) => {
      list.replaceChildren();
      if (!entries || entries.length === 0) {
        const empty = document.createElement("div");
        empty.className = "empty-state";
        empty.dataset.logsEmpty = "";
        empty.textContent = emptyText;
        list.appendChild(empty);
        return;
      }
      const frag = document.createDocumentFragment();
      for (const e of entries) {
        const row = document.createElement("div");
        row.className = "log-row log-" + (e.level || "info");
        row.dataset.seq = String(e.seq || 0);

        const ts = document.createElement("span");
        ts.className = "log-time mono";
        ts.title = e.iso || "";
        ts.textContent = e.time || "";

        const lvl = document.createElement("span");
        lvl.className = "log-lvl mono";
        lvl.textContent = e.badge || "";

        const body = document.createElement("div");
        body.className = "log-body";
        const msg = document.createElement("span");
        msg.className = "log-msg";
        msg.textContent = e.message || "";
        body.appendChild(msg);

        if (Array.isArray(e.attrs) && e.attrs.length > 0) {
          const attrs = document.createElement("span");
          attrs.className = "log-attrs";
          for (const a of e.attrs) {
            const pill = document.createElement("span");
            pill.className = "log-attr mono";
            const k = document.createElement("em");
            k.textContent = (a.key || "") + "=";
            pill.appendChild(k);
            pill.appendChild(document.createTextNode(a.value || ""));
            attrs.appendChild(pill);
          }
          body.appendChild(attrs);
        }

        row.appendChild(ts);
        row.appendChild(lvl);
        row.appendChild(body);
        frag.appendChild(row);
      }
      list.appendChild(frag);
    };

    const updateCounts = (counts) => {
      if (!counts) return;
      for (const key of ["total", "error", "warn", "info"]) {
        const el = document.querySelector(`[data-log-count="${key}"]`);
        if (el && typeof counts[key] === "number") el.textContent = String(counts[key]);
      }
      const tabMap = { all: counts.total, error: counts.error, warn: counts.warn, info: counts.info, debug: counts.debug };
      for (const [k, v] of Object.entries(tabMap)) {
        const el = box.querySelector(`[data-tab-count="${k}"]`);
        if (el && typeof v === "number") el.textContent = String(v);
      }
    };

    const poll = async () => {
      if (!toggle.checked || inFlight || document.hidden || signal?.aborted) return;
      inFlight = true;
      try {
        const res = await fetch(buildTailURL(), { credentials: "same-origin", signal });
        if (!res.ok) return;
        const data = await res.json();
        updateCounts(data.counts);
        renderEntries(data.entries);
        box.dataset.head = String(data.head || 0);
      } catch {
        /* ignore abort or transient fetch failure */
      } finally {
        inFlight = false;
      }
    };

    const schedule = () => {
      clearInterval(timer);
      timer = 0;
      if (toggle.checked && !signal?.aborted) {
        timer = setInterval(poll, 3000);
      }
    };

    toggle.addEventListener("change", () => {
      schedule();
      if (toggle.checked) poll();
    }, signal ? { signal } : undefined);

    if (signal) {
      signal.addEventListener("abort", () => {
        clearInterval(timer);
        timer = 0;
      }, { once: true });
    }
    schedule();
  };

  if (window.HuntDeck && typeof window.HuntDeck.onMount === "function") {
    window.HuntDeck.onMount(init);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => init(document), { once: true });
  } else {
    init(document);
  }
})();
