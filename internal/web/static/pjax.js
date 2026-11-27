// Soft shell-preserving navigation (PJAX + hover prefetch).
// Keeps <aside class="sidebar"> and stylesheets alive in memory while swapping
// <main class="content" id="main"> cleanly on same-origin HTML links and GET forms.
(() => {
  const mounts = [];
  const loadedScripts = new Set();
  const prefetchCache = new Map(); // normalized URL -> { html, finalURL, ts }
  const PREFETCH_TTL = 8000;
  const PREFETCH_MAX = 16;
  const HOVER_DELAY = 65;

  let pageAbort = new AbortController();
  let navFetchAbort = null;
  let navSeq = 0;
  let barTimer = 0;
  let hoverTimer = 0;

  // Record scripts already present on initial load so later swaps don't re-run them.
  document.addEventListener("DOMContentLoaded", () => {
    for (const s of document.querySelectorAll("script[src]")) {
      try {
        loadedScripts.add(new URL(s.src, location.origin).pathname);
      } catch {
        /* ignore */
      }
    }
  }, { once: true });

  function getBar() {
    let bar = document.getElementById("navbar");
    if (!bar && document.body) {
      bar = document.createElement("div");
      bar.id = "navbar";
      document.body.append(bar);
    }
    return bar;
  }

  function startBar() {
    clearTimeout(barTimer);
    barTimer = setTimeout(() => {
      const bar = getBar();
      if (bar) bar.classList.add("on");
    }, 90);
  }

  function stopBar() {
    clearTimeout(barTimer);
    barTimer = 0;
    const bar = document.getElementById("navbar");
    if (bar) bar.classList.remove("on");
  }

  function normURL(raw) {
    try {
      const u = new URL(raw, location.href);
      return u.origin + u.pathname + u.search;
    } catch {
      return "";
    }
  }

  function isEligibleURL(u) {
    if (u.origin !== location.origin) return false;
    if (u.protocol !== "http:" && u.protocol !== "https:") return false;
    if (/^\/(?:api\/|mcp\/rpc|login(?:$|[/?])|logout(?:$|[/?])|i18n\.js|static\/|feed\.atom|export(?:$|[/?]))/i.test(u.pathname)) {
      return false;
    }
    if (/\.(?:csv|json|txt|xml|atom|zip|gz|png|jpe?g|webp|svg|ico|pdf)$/i.test(u.pathname)) {
      return false;
    }
    return true;
  }

  function canSoftClick(a) {
    if (!a || a.target || a.hasAttribute("download") || a.closest("[data-no-pjax]")) return false;
    let u;
    try {
      u = new URL(a.href, location.href);
    } catch {
      return false;
    }
    if (!isEligibleURL(u)) return false;
    // Same path & query with hash (or empty hash) jumps natively within page.
    if (u.pathname === location.pathname && u.search === location.search && u.hash) return false;
    return true;
  }

  function setCache(key, entry) {
    if (prefetchCache.has(key)) prefetchCache.delete(key);
    prefetchCache.set(key, entry);
    while (prefetchCache.size > PREFETCH_MAX) {
      const oldest = prefetchCache.keys().next().value;
      prefetchCache.delete(oldest);
    }
  }

  function getCached(key) {
    const entry = prefetchCache.get(key);
    if (!entry) return null;
    if (Date.now() - entry.ts > PREFETCH_TTL) {
      prefetchCache.delete(key);
      return null;
    }
    return entry;
  }

  function syncSidebar(nextDoc, finalURL) {
    const curSidebar = document.querySelector(".sidebar");
    if (!curSidebar) return;

    const nextActive = nextDoc.querySelector(".sidebar .nav [aria-current='page']");
    const activeHref = nextActive ? nextActive.getAttribute("href") : "";

    for (const a of curSidebar.querySelectorAll(".nav a[href]")) {
      const match = Boolean(activeHref && a.getAttribute("href") === activeHref);
      if (match) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    }

    // Sync collapsible System / Integrations drawers: expand if active, else follow stored preference.
    for (const btn of curSidebar.querySelectorAll("[data-nav-toggle-section]")) {
      const name = btn.dataset.navToggleSection;
      const sec = curSidebar.querySelector(`[data-nav-section="${name}"]`);
      if (!sec) continue;
      const head = btn.closest(".nav-group-head");
      const hasActive = Boolean(
        sec.querySelector('[aria-current="page"]') ||
        head?.querySelector('[aria-current="page"]')
      );
      let open = false;
      if (hasActive) {
        open = true;
      } else {
        try {
          open = localStorage.getItem("huntdeck-nav-section:" + name) === "open";
        } catch {
          open = false;
        }
      }
      sec.hidden = !open;
      btn.setAttribute("aria-expanded", String(open));
    }

    const langNext = curSidebar.querySelector('.lang-switch input[name="next"]');
    if (langNext) langNext.value = finalURL.pathname + finalURL.search;

    if (curSidebar.classList.contains("nav-open")) {
      curSidebar.classList.remove("nav-open");
      curSidebar.querySelector("[data-nav-toggle]")?.setAttribute("aria-expanded", "false");
    }
  }

  function loadScriptOnce(src) {
    let pathname;
    try {
      pathname = new URL(src, location.origin).pathname;
    } catch {
      return Promise.resolve();
    }
    if (loadedScripts.has(pathname)) return Promise.resolve();
    return new Promise((resolve) => {
      const s = document.createElement("script");
      s.src = src;
      s.addEventListener("load", () => {
        loadedScripts.add(pathname);
        resolve();
      }, { once: true });
      s.addEventListener("error", () => resolve(), { once: true });
      document.head.append(s);
    });
  }

  function cleanupTransients() {
    for (const el of document.querySelectorAll(".drp-pop, .focus-tip")) {
      el.remove();
    }
  }

  function safeMount(fn, root, signal) {
    try {
      fn(root, signal);
    } catch (err) {
      if (typeof console !== "undefined" && console.error) console.error(err);
    }
  }

  async function navigate(rawURL, opts = {}) {
    const { push = true, replace = false } = opts;
    let targetURL;
    try {
      targetURL = new URL(rawURL, location.href);
    } catch {
      location.href = rawURL;
      return;
    }
    if (!isEligibleURL(targetURL)) {
      location.href = targetURL.href;
      return;
    }

    const seq = ++navSeq;
    if (navFetchAbort) navFetchAbort.abort();
    const fetchAbort = new AbortController();
    navFetchAbort = fetchAbort;
    document.documentElement.classList.add("pjax-loading");
    startBar();

    try {
      const cacheKey = normURL(targetURL.href);
      let html = "";
      let finalHref = targetURL.href;
      const cached = cacheKey && getCached(cacheKey);
      if (cached) {
        prefetchCache.delete(cacheKey);
        html = cached.html;
        finalHref = cached.finalURL;
      } else {
        const res = await fetch(targetURL.href, {
          headers: { Accept: "text/html", "X-HuntDeck-Nav": "1" },
          credentials: "same-origin",
          signal: fetchAbort.signal,
        });
        if (seq !== navSeq) return;
        const ctype = (res.headers.get("Content-Type") || "").toLowerCase();
        if (!ctype.includes("text/html")) {
          location.href = res.url || targetURL.href;
          return;
        }
        finalHref = res.url || targetURL.href;
        html = await res.text();
      }
      if (seq !== navSeq) return;

      const finalURL = new URL(finalHref, location.href);
      if (finalURL.origin !== location.origin || /^\/login(?:$|[/?])/.test(finalURL.pathname)) {
        location.href = finalURL.href;
        return;
      }

      const nextDoc = new DOMParser().parseFromString(html, "text/html");
      const nextMain = nextDoc.getElementById("main");
      const curMain = document.getElementById("main");
      if (!nextMain || !curMain || !nextDoc.querySelector(".sidebar")) {
        location.href = finalURL.href;
        return;
      }
      // Modules (e.g., Settings > Browser noVNC) need a native document load.
      if (nextMain.querySelector('script[type="module"]')) {
        location.href = finalURL.href;
        return;
      }

      const prevPath = location.pathname;
      const extraScripts = [];
      for (const s of nextMain.querySelectorAll("script[src]")) {
        const src = s.getAttribute("src");
        if (src) extraScripts.push(src);
        s.remove();
      }
      // Never inject inline scripts from swapped payloads.
      for (const s of nextMain.querySelectorAll("script")) {
        s.remove();
      }

      pageAbort.abort();
      pageAbort = new AbortController();
      cleanupTransients();

      const nextPage = nextMain.querySelector(".page");
      if (nextPage) nextPage.classList.add("pjax-in");

      curMain.replaceWith(document.adoptNode(nextMain));
      if (nextDoc.title) document.title = nextDoc.title;
      if (nextDoc.body?.dataset?.page) document.body.dataset.page = nextDoc.body.dataset.page;
      else delete document.body.dataset.page;

      const historyURL = finalURL.pathname + finalURL.search + (targetURL.hash && !finalURL.hash ? targetURL.hash : finalURL.hash);
      if (replace) history.replaceState({ pjax: true }, "", historyURL);
      else if (push) history.pushState({ pjax: true }, "", historyURL);

      syncSidebar(nextDoc, finalURL);

      // Snapshot mounts registered before any unseen page script loads, so a
      // newly loaded script's own onMount() call doesn't double-fire.
      const existingMounts = mounts.slice();
      for (const fn of existingMounts) {
        safeMount(fn, nextMain, pageAbort.signal);
      }
      for (const src of extraScripts) {
        await loadScriptOnce(src);
        if (seq !== navSeq) return;
      }

      const destHash = (targetURL.hash || finalURL.hash || "").slice(1);
      if (destHash) {
        const target = document.getElementById(decodeURIComponent(destHash));
        if (target) target.scrollIntoView({ behavior: "auto" });
      } else if (finalURL.pathname !== prevPath) {
        window.scrollTo(0, 0);
      }
    } catch (err) {
      if (err && err.name === "AbortError") return;
      location.href = targetURL.href;
    } finally {
      if (seq === navSeq) {
        document.documentElement.classList.remove("pjax-loading");
        stopBar();
      }
    }
  }

  async function prefetch(rawURL) {
    let u;
    try {
      u = new URL(rawURL, location.href);
    } catch {
      return;
    }
    if (!isEligibleURL(u)) return;
    if (u.pathname === location.pathname && u.search === location.search) return;
    const key = normURL(u.href);
    if (!key || getCached(key)) return;
    try {
      const res = await fetch(u.href, {
        headers: { Accept: "text/html", "X-HuntDeck-Nav": "1" },
        credentials: "same-origin",
      });
      if (!res.ok) return;
      const ctype = (res.headers.get("Content-Type") || "").toLowerCase();
      if (!ctype.includes("text/html")) return;
      const html = await res.text();
      setCache(key, { html, finalURL: res.url || u.href, ts: Date.now() });
    } catch {
      /* ignore speculative prefetch failure */
    }
  }

  window.HuntDeck = {
    onMount(fn) {
      if (typeof fn !== "function") return;
      mounts.push(fn);
      const run = () => safeMount(fn, document.getElementById("main") || document.body, pageAbort.signal);
      if (document.readyState === "loading") {
        // Inline scripts inside #main can call onMount before DOMContentLoaded; run right away.
        if (document.currentScript && document.currentScript.closest("#main")) run();
        else document.addEventListener("DOMContentLoaded", run, { once: true });
      } else {
        run();
      }
    },
    navigate,
    prefetch,
    signal() {
      return pageAbort.signal;
    },
  };

  // Intercept eligible link clicks.
  document.addEventListener("click", (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest("a[href]");
    if (!canSoftClick(a)) return;
    e.preventDefault();
    navigate(a.href, { push: true });
  });

  // Intercept GET form submissions (filters, omni search, date range picker).
  document.addEventListener("submit", (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement)) return;
    const method = (form.getAttribute("method") || "get").toLowerCase();
    if (method === "post") {
      prefetchCache.clear();
      return;
    }
    if (e.defaultPrevented || form.target || form.hasAttribute("data-no-pjax") || form.hasAttribute("data-try")) return;
    let action;
    try {
      action = new URL(form.getAttribute("action") || location.href, location.href);
    } catch {
      return;
    }
    if (!isEligibleURL(action)) return;
    const fd = new FormData(form);
    const sub = e.submitter;
    if (sub && sub.name && !fd.has(sub.name)) fd.append(sub.name, sub.value || "");
    const q = new URLSearchParams();
    for (const [k, v] of fd) {
      if (typeof v === "string") q.append(k, v);
    }
    action.search = q.toString();
    e.preventDefault();
    navigate(action.href, { push: true });
  });

  // Hover prefetch on navigation & tab controls.
  document.addEventListener("pointerenter", (e) => {
    if (!(e.target instanceof Element)) return;
    const a = e.target.closest(".nav a[href], .rsubnav a[href], .segs a[href], .tabs a[href], .chips a[href]");
    if (!canSoftClick(a)) return;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => prefetch(a.href), HOVER_DELAY);
  }, true);

  document.addEventListener("pointerleave", (e) => {
    if (!(e.target instanceof Element)) return;
    if (e.target.closest(".nav a[href], .rsubnav a[href], .segs a[href], .tabs a[href], .chips a[href]")) {
      clearTimeout(hoverTimer);
      hoverTimer = 0;
    }
  }, true);

  // Back / forward browser navigation.
  window.addEventListener("popstate", () => {
    if (isEligibleURL(new URL(location.href))) {
      navigate(location.href, { push: false });
    }
  });
})();
