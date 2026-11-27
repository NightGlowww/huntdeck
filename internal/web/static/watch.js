// Kept in this browser (localStorage), never sent anywhere but in the
// links it makes, so they need no account and work on a read-only copy
// such as the demo:
// - The watchlist: program pages' Watch button adds a program; the rail's
//   "Your watchlist" (watch.go) then keeps Programs and Changes to the
//   watched programs, with ?watch=; lists star the watched ones' names.
// - "New since your last visit": changes newer than the last visit are
//   marked (rows with data-at), and Changes says how many. A visit ends
//   after half an hour without a page view; marks stay until the next.
(() => {
  const WATCH = "huntdeck.watch", VISIT = "huntdeck.visit", MAX = 200, IDLE = 30 * 60 * 1000;
  const read = (k) => { try { return JSON.parse(localStorage.getItem(k)); } catch { return null; } };
  const write = (k, v) => { try { localStorage.setItem(k, JSON.stringify(v)); } catch { /* storage off or full */ } };
  const watched = () => { const v = read(WATCH); return Array.isArray(v) ? v.filter((k) => typeof k === "string").slice(0, MAX) : []; };

  // The watchlist's links and marks, from the list as stored now.
  const show = () => {
    const list = watched();
    for (const btn of document.querySelectorAll("[data-watch]")) {
      const on = list.includes(btn.dataset.watch);
      btn.hidden = false;
      btn.setAttribute("aria-pressed", String(on));
      btn.querySelector(".label").textContent = t(on ? "Watching" : "Watch");
    }
    for (const a of document.querySelectorAll("[data-watch-link]")) {
      const on = a.dataset.watchLink === "on";
      if (!on) {
        const u = new URL(location.href);
        u.searchParams.delete("page");
        u.searchParams.set("watch", list.join(","));
        a.href = u.pathname + u.search;
      }
      const em = a.querySelector("em");
      const n = on ? (new URL(location.href).searchParams.get("watch") || "").split(",").filter(Boolean).length : list.length;
      if (em) em.textContent = n ? String(n) : "";
      if (!on) a.hidden = list.length === 0;
      // The group shows while any of its links does.
      const group = a.closest("[data-watch-group]");
      if (group) group.hidden = [...group.querySelectorAll("a")].every((x) => x.hidden);
    }
    const paths = new Set(list.map((k) => "/programs/" + k));
    for (const a of document.querySelectorAll("main .pn > a.name")) {
      a.parentElement.classList.toggle("watched", paths.has(new URL(a.href).pathname));
    }
  };

  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-watch]");
    if (!btn) return;
    const key = btn.dataset.watch, list = watched();
    const i = list.indexOf(key);
    if (i >= 0) list.splice(i, 1);
    else if (list.length < MAX) list.push(key);
    write(WATCH, list);
    show();
  });
  window.addEventListener("storage", (e) => { if (e.key === WATCH) show(); }); // another tab

  // The last visit: prev is when the one before this ended.
  const now = Date.now();
  let visit = read(VISIT);
  if (!visit || typeof visit.last !== "number") visit = { prev: 0, last: now };
  else if (now - visit.last > IDLE) visit = { prev: visit.last, last: now };
  else visit.last = now;
  write(VISIT, visit);

  const run = () => {
    show();
    if (!visit.prev) return; // a first visit: nothing is "new since"
    let n = 0, rows = 0;
    for (const el of document.querySelectorAll("main [data-at]")) {
      const at = Date.parse(el.dataset.at);
      if (at > visit.prev) {
        el.classList.add("since-visit");
        if (!el.title) el.title = t("New since your last visit");
        if (el.matches("tr")) n++;
      }
      if (el.matches("tr")) rows++;
    }
    // Every row new, and more on the next page: more than these are new.
    const more = n === rows && document.querySelector('.pager a[rel="next"]') ? "+" : "";
    const note = document.querySelector("[data-since-note]");
    if (note && n) {
      const when = new Date(visit.prev);
      note.textContent = `${n}${more} new since your last visit`;
      note.title = t("Your last visit ended %s (remembered in this browser)", when.toLocaleString());
      note.hidden = false;
    }
  };
  if (window.HuntDeck) window.HuntDeck.onMount(run);
  else run();
})();
