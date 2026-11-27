// Column pickers (columns.go): show, hide and reorder a table's columns.
// The Programs list and the hunt board's fresh targets use one each. The
// menu (data-colpick="name") drives the table with data-cols="name"; each
// of the table's cells has a c-<key> class. One column (th[data-col-fixed])
// always shows, first; one without a width keeps at least data-col-min
// pixels. Columns under one heading (th[data-grp]) move as a
// group and reorder among themselves. Rows move by dragging their handle,
// or with the arrow keys on it.
//
// Kept in this browser, per table: which columns differ from the defaults
// (so everything else still follows the screen or box width), and the
// order, when it isn't the page's own. The table's data-hide and data-show
// carry the choices to the CSS, and each chosen cell gets col-hide or
// col-show. Loaded right after its table, without defer, so the columns
// are in place before the page first paints.
(() => {
  const init = (_root, signal) => {
  for (const box of document.querySelectorAll("[data-colpick]")) {
    if (box.dataset.colpickReady) continue;
    const name = box.dataset.colpick;
    const table = document.querySelector(`table[data-cols="${name}"]`);
    if (!table) continue;
    box.dataset.colpickReady = "1";
    picker(box, table, name, signal);
  }
  };

  function picker(box, table, name, signal) {
    const showKey = `huntdeck-${name}-columns-v2`;
    const orderKey = `huntdeck-${name}-column-order`;
    const menu = box.querySelector(".menu");
    const btn = box.querySelector("[data-colpick-toggle]");
    const top = menu.querySelector("[data-corder]");
    const lists = [...menu.querySelectorAll("[data-corder]")];
    const picks = [...menu.querySelectorAll("input[type=checkbox]")];
    const load = (k) => {
      try { return JSON.parse(localStorage.getItem(k) || "null"); } catch (e) { return null; }
    };
    const store = (k, v) => {
      try {
        if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, JSON.stringify(v));
      } catch (e) { /* ignore */ }
    };
    const keyOf = (cell) => {
      for (const n of cell.classList) if (n.startsWith("c-")) return n.slice(2);
      return "";
    };
    const cellsOf = (tr) => {
      const m = {};
      for (const c of tr.children) { const k = keyOf(c); if (k) m[k] = c; }
      return m;
    };
    const head = table.tHead.rows[0];
    const sub = table.tHead.rows[1] || null;
    const grp = table.querySelector("th[data-grp]");

    // ---- Order ----
    // flat lists the column keys in the menu's order, groups expanded.
    const flat = () => [...top.children].flatMap((li) =>
      li.classList.contains("cgroup") ? [...li.querySelector("ol").children].map((c) => c.dataset.key) : [li.dataset.key]);
    const defaultOrder = flat();
    // merge checks a saved order against the columns on the page: unknown
    // keys are dropped, new columns go after the one they follow by default.
    const merge = (saved) => {
      if (!Array.isArray(saved)) return defaultOrder.slice();
      const out = [...new Set(saved.filter((k) => defaultOrder.includes(k)))];
      defaultOrder.forEach((k, i) => {
        if (!out.includes(k)) out.splice(i ? out.indexOf(defaultOrder[i - 1]) + 1 : 0, 0, k);
      });
      return out;
    };
    // arrange puts the menu's rows in an order; a group sits where its
    // first column does.
    const arrange = (order) => {
      const rank = (li) => (li.classList.contains("cgroup")
        ? Math.min(...[...li.querySelector("ol").children].map((c) => order.indexOf(c.dataset.key)))
        : order.indexOf(li.dataset.key));
      for (const ol of lists) [...ol.children].sort((a, b) => rank(a) - rank(b)).forEach((li) => ol.append(li));
    };
    // place moves the table's cells into the menu's order, after the ones
    // the menu doesn't list (the fixed column, a row's checkbox).
    const place = () => {
      const keys = flat();
      const h = cellsOf(head);
      const s = sub ? cellsOf(sub) : {};
      for (const li of top.children) {
        if (!li.classList.contains("cgroup")) { if (h[li.dataset.key]) head.append(h[li.dataset.key]); continue; }
        if (grp) head.append(grp);
        for (const c of li.querySelector("ol").children) if (s[c.dataset.key]) sub.append(s[c.dataset.key]);
      }
      for (const body of table.tBodies) {
        for (const tr of body.rows) {
          const c = cellsOf(tr);
          for (const k of keys) if (c[k]) tr.append(c[k]);
        }
      }
    };
    const saveOrder = () => {
      const now = flat();
      store(orderKey, now.join() === defaultOrder.join() ? null : now);
    };

    // ---- Shown and hidden ----
    let chosen = load(showKey);
    if (typeof chosen !== "object" || chosen === null || Array.isArray(chosen)) chosen = {};
    const shows = (k) => {
      const th = table.tHead.querySelector("th.c-" + k);
      return !!th && getComputedStyle(th).display !== "none";
    };
    const mark = () => {
      for (const row of table.rows) {
        for (const c of row.children) {
          const k = keyOf(c);
          c.classList.toggle("col-hide", chosen[k] === "hide");
          c.classList.toggle("col-show", chosen[k] === "show");
        }
      }
    };
    // fit keeps the column that takes the spare room readable: with more
    // columns on than there is room for, the table grows wider and scrolls
    // sideways instead of squeezing it away.
    const flex = table.tHead.querySelector("th[data-col-min]");
    // A table that does not fit its column is clipped by the narrow-width
    // stylesheet (`overflow-x: clip`) unless it says `is-wide`. That has nothing
    // to do with the min-width below: the poll history has no flex column and was
    // clipped with no way to scroll to its right side.
    const wide = () => {
      const wrap = table.closest(".table-wrap");
      if (wrap) {
        const overflows = table.style.minWidth !== "" || table.scrollWidth > wrap.clientWidth + 1;
        wrap.classList.toggle("is-wide", overflows);
        // A wrapper that has to scroll cannot hold a heading pinned to the
        // window: that is what put the poll history's header across its rows.
        wrap.classList.toggle("fits", !overflows);
      }
    };
    const fit = () => {
      table.style.minWidth = "";
      if (!flex) {
        wide();
        return;
      }
      if (!flex.getClientRects().length) {
        wide(); // rows as cards: no headings to keep
        return;
      }
      const short = Number(flex.dataset.colMin) - flex.offsetWidth;
      if (short > 0) table.style.minWidth = table.offsetWidth + short + "px";
      wide();
    };
    const apply = () => {
      table.dataset.hide = Object.keys(chosen).filter((k) => chosen[k] === "hide").join(" ");
      table.dataset.show = Object.keys(chosen).filter((k) => chosen[k] === "show").join(" ");
      mark();
      // A group's heading spans whichever of its columns show.
      if (grp) {
        const n = grp.dataset.grp.split(" ").filter(shows).length;
        grp.hidden = n === 0;
        grp.colSpan = Math.max(n, 1);
        grp.style.setProperty("--grp-cols", String(n));
      }
      fit();
    };
    const refresh = () => { for (const p of picks) p.checked = shows(p.value); };
    for (const p of picks) {
      p.addEventListener("change", () => {
        delete chosen[p.value];
        apply();
        if (shows(p.value) !== p.checked) chosen[p.value] = p.checked ? "show" : "hide";
        store(showKey, chosen);
        apply();
        refresh();
      });
    }

    // ---- Dragging ----
    let drag = null;
    const moved = () => { saveOrder(); place(); apply(); };
    menu.addEventListener("pointerdown", (e) => {
      const grip = e.target.closest(".grip");
      if (!grip || grip.tagName !== "BUTTON" || e.button !== 0 || drag) return;
      e.preventDefault();
      const li = grip.closest("li");
      grip.setPointerCapture(e.pointerId);
      drag = { li, list: li.parentElement };
      li.classList.add("dragging");
      box.classList.add("dragging");
    });
    menu.addEventListener("pointermove", (e) => {
      if (!drag) return;
      // It goes before the first row whose middle is below the pointer.
      let before = null;
      for (const s of drag.list.children) {
        if (s === drag.li) continue;
        const r = s.getBoundingClientRect();
        if (e.clientY < r.top + r.height / 2) { before = s; break; }
      }
      if (before !== drag.li.nextElementSibling) {
        drag.list.insertBefore(drag.li, before);
        moved();
      }
    });
    const end = () => {
      if (!drag) return;
      drag.li.classList.remove("dragging");
      box.classList.remove("dragging");
      drag = null;
    };
    menu.addEventListener("pointerup", end);
    menu.addEventListener("pointercancel", end);
    menu.addEventListener("keydown", (e) => {
      const grip = e.target.closest(".grip");
      if (!grip || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
      e.preventDefault();
      const li = grip.closest("li");
      if (e.key === "ArrowUp" && li.previousElementSibling) li.parentElement.insertBefore(li, li.previousElementSibling);
      else if (e.key === "ArrowDown" && li.nextElementSibling) li.parentElement.insertBefore(li.nextElementSibling, li);
      else return;
      grip.focus();
      moved();
    });

    // ---- The menu ----
    box.querySelector("[data-colpick-reset]").addEventListener("click", () => {
      chosen = {};
      store(showKey, null);
      store(orderKey, null);
      arrange(defaultOrder);
      place();
      apply();
      refresh();
    });
    const show = (open) => {
      if (open) refresh();
      menu.hidden = !open;
      btn.setAttribute("aria-expanded", String(open));
    };
    btn.addEventListener("click", () => show(menu.hidden));
    document.addEventListener("click", (e) => { if (!box.contains(e.target)) show(false); }, { signal });
    document.addEventListener("keydown", (e) => { if (e.key === "Escape") show(false); }, { signal });
    window.addEventListener("resize", () => { apply(); if (!menu.hidden) refresh(); }, { signal });
    // The board's boxes change width without the window doing so.
    if (window.ResizeObserver) {
      const ro = new ResizeObserver(() => fit());
      ro.observe(table.parentElement);
      signal.addEventListener("abort", () => ro.disconnect(), { once: true });
    }

    const saved = load(orderKey);
    if (saved) {
      arrange(merge(saved));
      place();
    }
    apply();
    box.hidden = false;
  }
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init(document, new AbortController().signal);
})();
