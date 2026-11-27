// Progressive enhancements only: every page works without JavaScript.

// Copy buttons: data-copy="#id" copies that element's text or value,
// data-copy-text the attribute's own value, data-copy-list="#id" the
// values of the list's checkboxes (only the ticked ones with data-checked),
// and data-copy-url what that address gives (a whole list's export).
document.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-copy], [data-copy-text], [data-copy-list], [data-copy-url]");
  if (!btn) return;
  if (btn.dataset.copyUrl) {
    // A whole list can take a moment to arrive: say so instead of looking dead.
    busyLabel(btn, t("Copying…"));
    const got = fetch(btn.dataset.copyUrl, { headers: { Accept: "text/plain" } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => t.trim() + "\n");
    // A ClipboardItem given the promise keeps the click's permission while
    // the list loads (Safari's rule); elsewhere, write once it's here.
    const write = window.ClipboardItem
      ? navigator.clipboard.write([new ClipboardItem({ "text/plain": got.then((t) => new Blob([t], { type: "text/plain" })) })])
      : got.then((t) => navigator.clipboard.writeText(t));
    write.then(() => copied(btn), () => copied(btn, t("Couldn't copy")));
    return;
  }
  let text;
  if (btn.dataset.copyText !== undefined) {
    text = btn.dataset.copyText;
  } else if (btn.dataset.copyList) {
    const sel = "input.pick" + ("checked" in btn.dataset ? ":checked" : "");
    text = [...document.querySelectorAll(btn.dataset.copyList + " " + sel)].filter(shown).map((i) => i.value).join("\n");
  } else {
    const el = document.querySelector(btn.dataset.copy);
    if (!el) return;
    text = "value" in el ? el.value : el.textContent;
  }
  if (!text.trim()) return;
  navigator.clipboard.writeText(text.trim() + "\n").then(() => copied(btn));
});

// busyLabel says what a button is doing while it waits, on its label or its
// own text. Nothing is remembered: copied() replaces it with the outcome.
function busyLabel(btn, say) {
	const label = btn.querySelector(".label");
	if (label) {
		label.textContent = say;
		return;
	}
	btn.dataset.busyWas = btn.textContent;
	btn.textContent = say;
}

// copied says so on the button: in its label, or by a mark on an icon.
function copied(btn, say = t("Copied")) {
  const label = btn.querySelector(".label");
  if (!label) {
    btn.classList.add("copied");
    setTimeout(() => btn.classList.remove("copied"), 1200);
    return;
  }
  const was = label.textContent;
  label.textContent = say;
  setTimeout(() => { label.textContent = was; }, 1500);
}

// shown reports whether an element is laid out (rows only wide screens show aren't).
function shown(el) { return el.offsetParent !== null; }

// Ticking targets shows the selection bar; data-select-all ticks them all.
document.addEventListener("change", (e) => {
  const box = e.target;
  const list = box.closest("section, form");
  if (!list || !(box.matches("input.pick") || box.matches("[data-select-all]"))) return;
  const picks = [...list.querySelectorAll("input.pick")].filter(shown);
  if (box.matches("[data-select-all]")) for (const p of picks) p.checked = box.checked;
  const n = picks.filter((p) => p.checked).length;
  const bar = list.querySelector("[data-selbar]");
  if (bar) {
    bar.hidden = n === 0;
    bar.querySelector("[data-selcount]").textContent = t("%s selected", n);
  }
  const all = list.querySelector("[data-select-all]");
  if (all) {
    all.checked = n > 0 && n === picks.length;
    all.indeterminate = n > 0 && n < picks.length;
  }
});

// Re-submit with the original button so its name, value and formaction survive
// confirmation, including buttons associated with a form from outside it.
document.addEventListener("submit", (e) => {
  const form = e.target;
  const button = e.submitter;
  const message = button?.dataset.confirm || form.dataset.confirm;
  if (!message || e.defaultPrevented || form.dataset.confirmed) return;
  e.preventDefault();
  askInPage(message, button?.textContent.trim(), button?.classList.contains("danger")).then((yes) => {
    if (!yes || !form.isConnected || (button && button.form !== form)) return;
    form.dataset.confirmed = "1";
    try {
      form.requestSubmit(button || undefined);
    } finally {
      delete form.dataset.confirmed;
    }
  });
});

function askInPage(message, action, destructive = false) {
  if (document.querySelector("dialog.ask[open]")) return Promise.resolve(false);
  return new Promise((resolve) => {
    const dlg = document.createElement("dialog");
    dlg.className = "vdialog ask";
    const p = document.createElement("p");
    p.id = "confirm-question";
    p.textContent = message;
    dlg.setAttribute("aria-labelledby", p.id);
    const yes = document.createElement("button");
    yes.className = destructive ? "button danger" : "button";
    yes.textContent = action || t("Yes, do it");
    const no = document.createElement("button");
    no.className = "button secondary";
    no.textContent = t("Cancel");
    no.autofocus = true;
    const row = document.createElement("div");
    row.className = "ask-actions";
    row.append(no, yes);
    dlg.append(p, row);
    yes.addEventListener("click", () => dlg.close("confirm"));
    no.addEventListener("click", () => dlg.close());
    dlg.addEventListener("close", () => {
      dlg.remove();
      resolve(dlg.returnValue === "confirm");
    }, {once: true});
    document.body.append(dlg);
    dlg.showModal();
  });
}

// Wait for the submit event to finish: JS-handled forms manage their own
// state, and native submissions still need the clicked button's value.
const pendingForms = new Map();
document.addEventListener("submit", (e) => {
  const form = e.target;
  if (pendingForms.has(form)) {
    e.preventDefault();
    return;
  }
  const btn = e.submitter;
  if (!(btn instanceof HTMLButtonElement) || form.hasAttribute("data-no-busy")) return;
  setTimeout(() => {
    if (e.defaultPrevented || btn.disabled || !form.isConnected) return;
    const content = [...btn.childNodes];
    if (form.dataset.busy && !form.hasAttribute("data-no-pending")) btn.textContent = form.dataset.busy;
    btn.disabled = true;
    btn.classList.add("is-busy");
    form.setAttribute("aria-busy", "true");
    let timer;
    const reset = () => {
      clearTimeout(timer);
      btn.replaceChildren(...content);
      btn.disabled = false;
      btn.classList.remove("is-busy");
      form.removeAttribute("aria-busy");
      pendingForms.delete(form);
    };
    pendingForms.set(form, reset);
    if (!form.hasAttribute("data-stay-busy")) timer = setTimeout(reset, 8000);
  }, 0);
});
window.addEventListener("pageshow", () => {
  for (const reset of pendingForms.values()) reset();
});

// Theme: the top bar's menu sets light or dark ("system" follows the
// system) and the palette; Settings' picker sets the palette too. Both are
// kept in this browser (theme.js applies them on later pages) and every
// control shows the current choice.
(() => {
  const root = document.documentElement;
  const menu = document.querySelector("[data-theme-menu]");
  const settings = document.querySelector("[data-palettes]");
  const dark = window.matchMedia("(prefers-color-scheme: dark)");
  const names = { system: t("System"), light: t("Light"), dark: t("Dark") };
  const mode = () => root.dataset.theme || "system";
  const palette = () => root.dataset.palette || "midnight";
  const save = (key, v) => { try { localStorage.setItem(key, v); } catch (e) { /* ignore */ } };
  const show = () => {
    for (const r of document.querySelectorAll('input[name="tm-mode"]')) r.checked = r.value === mode();
    for (const r of document.querySelectorAll('input[name="tm-palette"], input[data-palette-pick]')) r.checked = r.value === palette();
    if (!menu) return;
    const btn = menu.querySelector("[data-theme-toggle]");
    const scheme = mode() === "system" ? names.system + " · " + (dark.matches ? "dark" : "light") : names[mode()];
    const pal = document.querySelector('input[name="tm-palette"]:checked');
    btn.title = t("Theme: %s", scheme + (pal ? ", " + pal.closest("label").querySelector(".tm-name").firstChild.textContent : ""));
    for (const icon of btn.querySelectorAll("[data-icon]")) icon.toggleAttribute("hidden", icon.dataset.icon !== mode()); // SVG: an attribute, not .hidden
  };
  document.addEventListener("change", (e) => {
    const r = e.target;
    if (r.name === "tm-mode") {
      if (r.value === "system") delete root.dataset.theme; else root.dataset.theme = r.value;
      save("huntdeck-theme", r.value);
    } else if (r.name === "tm-palette" || r.matches("input[data-palette-pick]")) {
      if (r.value === "midnight") delete root.dataset.palette; else root.dataset.palette = r.value;
      save("huntdeck-palette", r.value);
    } else return;
    show();
  });
  dark.addEventListener("change", show);
  if (settings) settings.hidden = false;
  if (menu) {
    const btn = menu.querySelector("[data-theme-toggle]");
    const pop = menu.querySelector(".tm-pop");
    const open = (on) => {
      pop.hidden = !on;
      btn.setAttribute("aria-expanded", String(on));
      if (on) (pop.querySelector("input:checked") || pop.querySelector("input")).focus();
    };
    btn.addEventListener("click", () => open(pop.hidden));
    document.addEventListener("click", (e) => { if (!pop.hidden && !menu.contains(e.target)) open(false); });
    menu.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && !pop.hidden) { open(false); btn.focus(); }
    });
    menu.addEventListener("focusout", (e) => { if (!pop.hidden && !menu.contains(e.relatedTarget) && e.relatedTarget) open(false); });
    menu.hidden = false;
  }
  show();
})();

// A read-only copy shows the user's own pages as a tour: every form that
// would change something is switched off (the server refuses them anyway).
if (document.documentElement.hasAttribute("data-read-only")) {
  for (const f of document.querySelectorAll('form[method="post" i]')) {
    if (f.action.endsWith("/api/v1/match")) continue; // only reads
    for (const el of f.elements) {
      el.disabled = true;
      el.title = t("Not available in this demo");
    }
  }
}

// A read-only copy (a demo) is seen from anywhere, so its pages give times
// in UTC ("2026-11-05 14:00 UTC"): here they're put in the visitor's own
// zone, in text and tooltips alike. Without scripts they stay in UTC.
if (document.documentElement.hasAttribute("data-read-only")) {
  const utc = /(\d{4})-(\d\d)-(\d\d) (\d\d):(\d\d) UTC\b/g;
  const pad = (n) => String(n).padStart(2, "0");
  const zone = new Intl.DateTimeFormat(undefined, { timeZoneName: "short" });
  const local = (text) => text.replace(utc, (_, y, mo, d, h, mi) => {
    const t = new Date(Date.UTC(+y, +mo - 1, +d, +h, +mi));
    const name = zone.formatToParts(t).find((p) => p.type === "timeZoneName")?.value || "";
    return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())} ${name}`.trim();
  });
  for (const el of document.querySelectorAll('[title*=" UTC"]')) el.title = local(el.title);
  const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    if (n.nodeValue.includes(" UTC")) n.nodeValue = local(n.nodeValue);
  }
}

// A CSV link (data-csv-cols="table") takes the columns its table shows, in
// their order (columns.js may have hidden or moved some): ?cols=.
document.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-csv-cols]");
  const row = a && document.querySelector(`table[data-cols="${a.dataset.csvCols}"] tbody tr`);
  if (!row) return;
  const keys = [...row.cells].filter(shown).map((td) => (td.className.match(/\bc-([a-z0-9]+)/) || [])[1])
    .filter((k) => k && k !== "prog" && k !== "check");
  const u = new URL(a.href, location.href);
  u.searchParams.set("cols", keys.join(","));
  a.href = u.pathname + u.search;
});

// A phone's search box can't show the whole hint: it gets a short one.
(() => {
  const box = document.getElementById("omni");
  if (!box) return;
  // The box you searched with shows the words, so they can be read, edited
  // and cleared from the same place on every page.
  if (!box.value) {
    const q = new URLSearchParams(location.search).get("q");
    if (q) box.value = q;
  }
  const long = box.placeholder;
  const narrow = matchMedia("(max-width: 700px)");
  const fit = () => { box.placeholder = narrow.matches ? t("Search, or paste a host") : long; };
  fit();
  narrow.addEventListener("change", fit);
  // Escape inside #omni clears typed text first; pressing it again blurs.
  box.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (box.value) {
      e.preventDefault();
      box.value = "";
    } else {
      box.blur();
    }
  });
})();

// Phones: the Menu button drops the page list down; a click elsewhere,
// Escape or picking a page closes it.
(() => {
  const btn = document.querySelector("[data-nav-toggle]");
  if (!btn) return;
  const bar = btn.closest(".sidebar");
  const open = (on) => {
    bar.classList.toggle("nav-open", on);
    btn.setAttribute("aria-expanded", String(on));
  };
  btn.addEventListener("click", () => open(!bar.classList.contains("nav-open")));
  document.addEventListener("click", (e) => { if (!bar.contains(e.target)) open(false); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && bar.classList.contains("nav-open")) { open(false); btn.focus(); }
  });
  btn.hidden = false;
})();

// A demo's banner hides for good in this browser (theme.js keeps it hidden).
(() => {
  const x = document.querySelector("[data-demo-banner-hide]");
  if (!x) return;
  x.addEventListener("click", () => {
    document.documentElement.dataset.demoBanner = "off";
    try { localStorage.setItem("huntdeck-demo-banner", "off"); } catch (e) { /* ignore */ }
  });
})();

// "/" focuses the top search bar, as on most hunting and code sites.
document.addEventListener("keydown", (e) => {
  if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
  const box = document.getElementById("omni");
  if (!box) return;
  e.preventDefault();
  box.focus();
  box.select();
});

// Charts: instant tooltips, "Oct 31 · $38,300". Chart points and bars
// (g.pt, g.bar) carry a <title>, which browsers show only after a delay:
// its text moves to data-tip and shows at once by the pointer. Trend and
// month charts (.tchart) carry their points in data-days instead ("x|y|tip"
// joined by ";", in viewBox units; "x0~x1|y|tip" for a flat stretch), with a line and dot marking the one
// nearest the pointer. Without scripts the titles show as before.
(() => {
  const tip = document.createElement("div");
  tip.className = "ctip";
  tip.hidden = true;
  document.body.append(tip);
  const show = (text, e) => {
    tip.textContent = text;
    tip.hidden = false;
    const pad = 12, w = tip.offsetWidth, h = tip.offsetHeight;
    let left = e.clientX + pad, top = e.clientY + pad;
    if (left + w > window.innerWidth - 4) left = e.clientX - pad - w;
    if (top + h > window.innerHeight - 4) top = e.clientY - pad - h;
    tip.style.left = Math.max(4, left) + "px";
    tip.style.top = Math.max(4, top) + "px";
  };
  const hide = () => { tip.hidden = true; };

  for (const t of document.querySelectorAll("svg :is(g.pt, g.bar) > title")) {
    t.parentNode.dataset.tip = t.textContent;
    t.remove();
  }
  document.addEventListener("pointermove", (e) => {
    const g = e.target instanceof Element ? e.target.closest("svg [data-tip]") : null;
    if (g) show(g.dataset.tip, e);
    else if (!e.target.closest?.(".tchart[data-days]")) hide();
  });
  document.addEventListener("pointerleave", hide);

  const svgNS = "http://www.w3.org/2000/svg";
  for (const chart of document.querySelectorAll(".tchart[data-days]")) {
    const svg = chart.querySelector("svg");
    // A stretch of days with one value is "x0~x1|y|tip": the mark follows
    // the pointer along it.
    const days = chart.dataset.days.split(";").map((p) => {
      const [x, y, text] = p.split("|");
      const [x0, x1 = x0] = x.split("~").map(Number);
      return { x0, x1, y: Number(y), text };
    });
    if (!svg || days.length < 1 || (days.length < 2 && days[0].x0 === days[0].x1) ||
      days.some((d) => Number.isNaN(d.x0) || Number.isNaN(d.x1))) continue;
    const off = (d, x) => (x < d.x0 ? d.x0 - x : x > d.x1 ? x - d.x1 : 0);
    chart.removeAttribute("title"); // the tooltip below replaces it
    const vb = svg.viewBox.baseVal;
    const vw = (vb && vb.width) || 100, vh = (vb && vb.height) || 30;
    // The chart is stretched to its cell, so the dot is an ellipse that
    // looks round once stretched; the line keeps its width (CSS).
    const mark = document.createElementNS(svgNS, "line");
    mark.setAttribute("class", "cmark");
    mark.setAttribute("y1", "0");
    mark.setAttribute("y2", String(vh));
    const dot = document.createElementNS(svgNS, "ellipse");
    dot.setAttribute("class", "cdot");
    const marks = [mark, dot];
    marks.forEach((m) => { m.style.display = "none"; svg.append(m); });
    chart.addEventListener("pointermove", (e) => {
      const box = svg.getBoundingClientRect();
      if (!box.width) return;
      const x = ((e.clientX - box.left) / box.width) * vw;
      let day = days[0];
      for (const d of days) if (off(d, x) < off(day, x)) day = d;
      const at = Math.min(Math.max(x, day.x0), day.x1);
      mark.setAttribute("x1", at);
      mark.setAttribute("x2", at);
      dot.setAttribute("cx", at);
      dot.setAttribute("cy", day.y);
      dot.setAttribute("rx", (3 * vw) / box.width);
      dot.setAttribute("ry", (3 * vh) / box.height);
      marks.forEach((m) => { m.style.display = ""; });
      show(day.text, e);
    });
    chart.addEventListener("pointerleave", () => {
      hide();
      marks.forEach((m) => { m.style.display = "none"; });
    });
  }
})();

// The filter rail's Filters button (narrower screens) folds its groups
// away or shows them, and the choice stays for the next page (theme.js
// applies it before first paint).
for (const btn of document.querySelectorAll("[data-filters-toggle]")) {
  const root = document.documentElement;
  const sync = () => btn.setAttribute("aria-expanded", String(root.dataset.filters === "open"));
  sync();
  btn.addEventListener("click", () => {
    const open = root.dataset.filters !== "open";
    if (open) root.dataset.filters = "open";
    else delete root.dataset.filters;
    try {
      if (open) localStorage.setItem("huntdeck-filters", "open");
      else localStorage.removeItem("huntdeck-filters");
    } catch (e) { /* storage blocked: for this page only */ }
    sync();
  });
  // Escape folds the rail away again, focus back on its button: the panel
  // sits at the top of the page, where a keyboard user starts.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || e.defaultPrevented) return;
    if (root.dataset.filters !== "open") return;
    if (e.target.closest("input, textarea, select, [contenteditable]")) return;
    delete root.dataset.filters;
    try { localStorage.removeItem("huntdeck-filters"); } catch (err) { /* storage blocked */ }
    sync();
    btn.focus();
  });
}

// Selects with data-autosubmit apply at once; their form's button is only
// needed without scripts.
for (const sel of document.querySelectorAll("select[data-autosubmit]")) {
  const btn = sel.form.querySelector("[data-autosubmit-button]");
  if (btn) btn.hidden = true;
  sel.addEventListener("change", () => sel.form.requestSubmit());
}

// Menus (details[data-menu]): a click outside or Escape closes them, and
// opening one closes the others.
(() => {
  const menus = () => document.querySelectorAll("details[data-menu][open]");
  document.addEventListener("click", (e) => {
    for (const m of menus()) if (!m.contains(e.target)) m.open = false;
  });
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    for (const m of menus()) { m.open = false; m.querySelector("summary")?.focus(); }
  });
})();

// A menu (details.tb-menu's .menu) opening near the screen's edge, as on
// a phone where the buttons wrap, is moved sideways to stay on screen.
document.addEventListener("toggle", (e) => {
  const d = e.target;
  if (!(d instanceof HTMLDetailsElement) || !d.matches(".tb-menu")) return;
  const m = d.querySelector(":scope > .menu");
  if (!m) return;
  m.style.translate = "";
  if (!d.open) return;
  const r = m.getBoundingClientRect(), edge = 8, vw = document.documentElement.clientWidth;
  const shift = r.left < edge ? edge - r.left : r.right > vw - edge ? vw - edge - r.right : 0;
  if (shift) m.style.translate = `${Math.round(shift)}px 0`;
}, true);

// Filter boxes (input[data-filter-rows="#table"]) hide the table's rows
// that don't contain what's typed, as it's typed; pressing Enter still
// asks the server, which searches every row, not only the ones shown.
for (const input of document.querySelectorAll("input[data-filter-rows]")) {
  const table = document.querySelector(input.dataset.filterRows);
  if (!table) continue;
  const rows = [...table.tBodies].flatMap((b) => [...b.rows]);
  // Hiding rows silently made a filter that matches nothing look like an
  // empty list: say how many are left, and let a screen reader hear it.
  let status = input.parentElement && input.parentElement.querySelector(".filter-count");
  if (!status) {
    status = document.createElement("span");
    status.className = "filter-count muted small";
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    input.insertAdjacentElement("afterend", status);
  }
  input.addEventListener("input", () => {
    const q = input.value.trim().toLowerCase();
    let shown = 0;
    for (const tr of rows) {
      tr.hidden = q !== "" && !tr.textContent.toLowerCase().includes(q);
      if (!tr.hidden) shown++;
    }
    status.textContent = q === "" ? "" : t("%d of %d shown", shown, rows.length);
  });
}

// Contents lists (data-toc): the link to the section being read is marked
// as it scrolls by.
for (const toc of document.querySelectorAll("[data-toc]")) {
  const links = [...toc.querySelectorAll('a[href^="#"]')];
  const targets = links.map((a) => document.getElementById(decodeURIComponent(a.hash.slice(1)))).filter(Boolean);
  if (!targets.length) continue;
  // A hidden section (the API page filters them) reports a zero rect, which
  // would make it look like the section at the top of the page.
  const shown = (el) => el.getClientRects().length > 0 && !el.hidden;
  const mark = () => {
    const line = 96; // just under the top bar
    const live = targets.filter(shown);
    if (!live.length) return;
    let current = live[0];
    for (const t of live) if (t.getBoundingClientRect().top <= line) current = t;
    for (const a of links) {
      const on = a.hash.slice(1) === current.id;
      a.classList.toggle("on", on);
      if (on && toc.scrollHeight > toc.clientHeight) {
        const r = a.getBoundingClientRect(), box = toc.getBoundingClientRect();
        if (r.top < box.top || r.bottom > box.bottom) toc.scrollTop += r.top - box.top - box.height / 3;
      }
    }
  };
  let queued = false;
  window.addEventListener("scroll", () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; mark(); });
  }, { passive: true });
  mark();
}

// Rows with data-href open it when clicked anywhere other than a link,
// button or field of their own, so a list's whole row is the target while
// tooltips and the row's other links keep working (as does a data-norow
// cell, such as a checkbox's). A middle, Ctrl or Cmd
// click opens a new tab, as a link would; data-ext rows (another site) always
// do, without sending this page as the referrer. Selecting text doesn't
// count as a click.
function rowLink(e) {
  if (document.documentElement.dataset.longpress) return; // the click after a hold
  const row = e.target.closest("tr[data-href], .nrow[data-href]");
  if (!row || e.target.closest("a, button, input, select, textarea, summary, label, [data-norow]")) return;
  if (row.closest(".board.editing")) return; // arranging the hunt board
  if (e.type === "click" && e.button !== 0) return;
  if (e.type === "auxclick" && e.button !== 1) return;
  if (String(getSelection())) return;
  const url = row.dataset.href;
  if ("ext" in row.dataset || e.type === "auxclick" || e.ctrlKey || e.metaKey) {
    window.open(url, "_blank", "noopener,noreferrer");
  } else {
    location.href = url;
  }
}
document.addEventListener("click", rowLink);
document.addEventListener("auxclick", rowLink);

// Live panels (data-live-refresh="seconds"), such as the poll history
// while a poll runs: the panel is read again from the page every so often
// while the tab is visible, until nothing in it is running.
for (const panel of document.querySelectorAll("[data-live-refresh]")) {
  const every = Math.max(5, Number(panel.dataset.liveRefresh) || 10) * 1000;
  let current = panel;
  const tick = async () => {
    if (document.hidden) return setTimeout(tick, every);
    try {
      const res = await fetch(location.href, { headers: { Accept: "text/html" }, cache: "no-store" });
      if (!res.ok) return setTimeout(tick, every * 3);
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      const next = current.id && doc.getElementById(current.id);
      if (!next) return;
      current.replaceWith(next);
      current = next;
      if (next.hasAttribute("data-live-refresh")) setTimeout(tick, every);
    } catch { setTimeout(tick, every * 3); }
  };
  setTimeout(tick, every);
}

// A short status line for things that leave no trace on the page: copying,
// or a download starting. Screen readers hear it too (role=status).
(() => {
  let el, timer;
  window.hdToast = (msg) => {
    if (!el) {
      el = document.createElement("div");
      el.className = "toast";
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      document.body.append(el);
    }
    el.textContent = msg;
    el.classList.add("on");
    clearTimeout(timer);
    timer = setTimeout(() => el.classList.remove("on"), 2400);
  };
  document.addEventListener("click", (e) => {
    const a = e.target.closest("a[data-export]");
    if (!a) return;
    hdToast(t("Download started"));
  });
})();
// Forms say what's wrong where it's wrong: a message under the field, in
// the page's language, focus on the first one; and a submit that passes
// marks its button busy so a double click can't post twice.
(() => {
  const message = (field) => {
    if (field.validity.valueMissing) return t("This field is required.");
    if (field.validity.typeMismatch) return t("That doesn't look like a valid value.");
    if (field.validity.patternMismatch) return t("Check the format of this value.");
    if (field.validity.rangeUnderflow || field.validity.rangeOverflow) return t("This value is out of range.");
    return t("Check this value.");
  };
  const show = (field) => {
    field.setAttribute("aria-invalid", "true");
    let note = field.parentElement.querySelector(".field-error");
    if (!note) {
      note = document.createElement("p");
      note.className = "field-error";
      note.setAttribute("role", "alert");
      field.insertAdjacentElement("afterend", note);
    }
    note.textContent = message(field);
  };
  const clear = (field) => {
    field.removeAttribute("aria-invalid");
    const note = field.parentElement?.querySelector(".field-error");
    if (note) note.remove();
  };
  document.addEventListener("invalid", (e) => show(e.target), true);
  document.addEventListener("input", (e) => {
    if (e.target.willValidate && e.target.checkValidity()) clear(e.target);
  }, true);
})();

// The Programs list's selection bar: ignore the ticked programs in one go.
(() => {
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-ignore-selected]");
    if (!btn) return;
    const table = document.querySelector("#programs-list");
    const refs = table ? [...table.querySelectorAll("input.pick:checked")].map((i) => i.value) : [];
    if (!refs.length) return;
    const to = btn.dataset.ignoreTo || "1";
    const ask = to === "1"
      ? t("Ignore %s programs? They leave the lists, picks and alerts; their pages stay.", refs.length)
      : t("Unignore %s programs? They come back to the lists, picks and alerts.", refs.length);
    const send = () => {
      const form = document.createElement("form");
      form.method = "post";
      form.action = "/programs/ignore";
      const add = (name, value) => {
        const i = document.createElement("input");
        i.type = "hidden";
        i.name = name;
        i.value = value;
        form.append(i);
      };
      for (const ref of refs) add("p", ref);
      add("ignored", to);
      add("back", location.pathname + location.search);
      document.body.append(form);
      form.requestSubmit();
    };
    // The question goes in the page: a suppressed window.confirm would make
    // this button do nothing at all.
    askInPage(ask).then((yes) => {
      if (yes) send();
    });
  });
})();

// A finger's way into a list's selection: press and hold a row to tick it,
// since the box itself is small on a phone. A press that moves is a scroll
// and is left alone, and the click that follows the hold doesn't open the row.
(() => {
  if (!matchMedia("(pointer: coarse)").matches) return;
  let timer = null;
  let start = null;
  document.addEventListener("pointerdown", (e) => {
    if (e.pointerType === "mouse") return;
    const row = e.target.closest("tr[data-href]");
    const box = row && row.querySelector("input.pick");
    if (!box) return;
    start = [e.clientX, e.clientY];
    timer = setTimeout(() => {
      timer = null;
      box.checked = !box.checked;
      box.dispatchEvent(new Event("change", { bubbles: true }));
      row.classList.add("longpress");
      setTimeout(() => row.classList.remove("longpress"), 260);
      document.documentElement.dataset.longpress = "1";
      setTimeout(() => delete document.documentElement.dataset.longpress, 700);
      if (navigator.vibrate) navigator.vibrate(12);
    }, 480);
  });
  document.addEventListener("pointermove", (e) => {
    if (!timer || !start) return;
    if (Math.abs(e.clientX - start[0]) > 8 || Math.abs(e.clientY - start[1]) > 8) {
      clearTimeout(timer);
      timer = null;
    }
  });
  const stop = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    start = null;
  };
  document.addEventListener("pointerup", stop);
  document.addEventListener("pointercancel", stop);
  document.addEventListener("contextmenu", (e) => {
    if (e.target.closest("tr[data-href] input.pick")) e.preventDefault();
  });
  // The click a finger sends when it lifts would open the row (or a link in
  // it): right after a hold it does nothing.
  document.addEventListener("click", (e) => {
    if (!document.documentElement.dataset.longpress) return;
    e.preventDefault();
    e.stopPropagation();
  }, true);
})();
// Back to the top: shown once a long page has been scrolled a screenful,
// and smooth unless the system asks for less motion.
(() => {
  const btn = document.querySelector("[data-to-top]");
  if (!btn) return;
  const reduced = matchMedia("(prefers-reduced-motion: reduce)");
  const sync = () => { btn.hidden = window.scrollY < 700; };
  sync();
  addEventListener("scroll", sync, { passive: true });
  btn.addEventListener("click", () => {
    window.scrollTo({ top: 0, behavior: reduced.matches ? "auto" : "smooth" });
    const main = document.getElementById("main");
    if (main) main.focus({ preventScroll: true });
  });
})();

// A page on its way: a thin bar under the top bar, after a moment, because a
// cold page can take half a second and every filter, sort and pager here is a
// plain link. Fast pages never show it.
(() => {
  const bar = document.createElement("div");
  bar.id = "navbar";
  document.body.append(bar);
  let timer;
  document.addEventListener("click", (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest("a[href]");
    if (!a || a.target || a.hasAttribute("download")) return;
    const url = new URL(a.href, location.href);
    if (url.origin !== location.origin) return;
    if (url.pathname === location.pathname && url.search === location.search) return;
    clearTimeout(timer);
    timer = setTimeout(() => bar.classList.add("on"), 150);
  }, true);
  addEventListener("pageshow", () => { clearTimeout(timer); bar.classList.remove("on"); });
})();

// Relative times are painted once by the server. Tick them, so a page left
// open does not keep saying "5m ago" an hour later, and keep the 7-day fresh
// highlight honest as it expires.
(() => {
  const at = document.querySelectorAll("[data-at]");
  if (!at.length) return;
  const say = (s) => {
    if (s < 60) return t("just now");
    const m = Math.floor(s / 60);
    if (m < 60) return t("%dm ago", m);
    const h = Math.floor(m / 60);
    if (h < 48) return t("%dh ago", h);
    return t("%dd ago", Math.floor(h / 24));
  };
  const tick = () => {
    const now = Date.now();
    for (const el of document.querySelectorAll("[data-at]")) {
      const when = Date.parse(el.dataset.at);
      if (Number.isNaN(when)) continue;
      const text = say((now - when) / 1000);
      // data-ago is the opt-in for rewriting the text: a feed row shows the
      // date there and only wants its 7-day mark kept honest.
      // data-ago marks what may be rewritten: on the element itself, or on its
      // .when child (the board's "last checked" spans mark the child).
      const child = el.querySelector(".when");
      const relative = el.hasAttribute("data-ago") || (child !== null && child.hasAttribute("data-ago"));
      const cell = el.matches(".when") ? el : el.querySelector(".when") ||
        (el.childElementCount === 0 ? el : null);
      if (cell) {
        if (relative) cell.textContent = text;
        cell.classList.toggle("fresh", now - when < 7 * 86400000);
      }
    }
  };
  tick();
  setInterval(tick, 30000);
})();

// Watch /api/v1/status while the page is visible with a single poll: update
// the in-flight poll pill and surface the reload hint when a newer poll
// finishes after this page loaded.
(() => {
  const pill = document.querySelector("[data-poll-watch]");
  const hint = document.querySelector("[data-reload-hint]");
  if (!pill && !hint) return;
  let baseline = null;
  const newest = (d) => {
    let t = 0;
    for (const p of d.last_polls || []) {
      if (!p.finished_at) continue;
      const at = Date.parse(p.finished_at);
      if (at > t) t = at;
    }
    return t;
  };
  const check = async () => {
    try {
      const res = await fetch("/api/v1/status", { headers: { Accept: "application/json" } });
      if (!res.ok) {
        window.hdConn?.fail();
        return;
      }
      window.hdConn?.ok();
      const d = await res.json();
      if (pill) {
        const running = (d.last_polls || []).some((p) => p.started_at && !p.finished_at);
        pill.hidden = !running;
      }
      if (hint) {
        const t = newest(d);
        if (t) {
          if (baseline === null) {
            baseline = t;
          } else {
            // Half a minute of slack: the page's own poll may be the one that
            // produced what it is showing.
            hint.hidden = !(t > baseline + 30000);
          }
        }
      }
    } catch {
      if (pill) window.hdConn?.fail();
    }
  };
  check();
  setInterval(() => { if (!document.hidden) check(); }, pill ? 15000 : 30000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) check(); });
})();

// The filter rail scrolls on its own on wide screens. Clicking a chip reloads the
// page, which used to put the rail back at the top and hide the chip you just
// set; keep where it was, and on a first visit bring the active chips into view.
(() => {
  const rail = document.querySelector(".plist > .facets");
  if (!rail) return;
  const key = "huntdeck-rail:" + location.pathname;
  let saved = null;
  try { saved = sessionStorage.getItem(key); } catch { /* private mode */ }
  if (saved !== null) {
    rail.scrollTop = Number(saved);
  } else {
    const on = rail.querySelectorAll(".fgroups .fc.on");
    const last = on.length ? on[on.length - 1] : null;
    if (last) last.scrollIntoView({ block: "nearest" });
  }
  rail.addEventListener("scroll", () => {
    try { sessionStorage.setItem(key, String(rail.scrollTop)); } catch { /* private mode */ }
  }, { passive: true });
})();

// The deliveries list shows a queue: it fills and empties while the page is
// open. Re-fetch it quietly every half minute and swap the table only when it
// changed, so a waiting row turns into "sent" without a reload.
(() => {
  const box = document.querySelector("#deliveries[data-refresh]");
  if (!box) return;
  const every = Number(box.dataset.refresh || 30) * 1000;
  const swap = async () => {
    if (document.hidden) return;
    try {
      const res = await fetch(location.href, { credentials: "same-origin" });
      if (!res.ok) return;
      const html = await res.text();
      const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("#deliveries");
      if (fresh && fresh.innerHTML !== box.innerHTML) box.innerHTML = fresh.innerHTML;
    } catch {
      /* offline or signed out: try again next time */
    }
  };
  setInterval(swap, every);
})();

// The API page lists every endpoint: 40 of them is a scroll, 80 is a search
// problem. The box narrows the nav and the sections together, and says how many
// are left.
(() => {
  const box = document.querySelector("input[data-filter-api]");
  if (!box) return;
  const nav = box.closest(".apinav");
  const directory = nav.querySelector("details");
  const compact = matchMedia("(max-width: 900px)");
  const fitDirectory = () => { directory.open = !compact.matches; };
  fitDirectory();
  compact.addEventListener("change", fitDirectory);
  const count = nav.querySelector(".filter-count");
  const links = [...nav.querySelectorAll('a[href^="#"]:not(.an-top)')];
  const groups = [...nav.querySelectorAll(".an-g")];
  const sections = [...document.querySelectorAll(".eps > section[id], .eps > h2.apigroup")];
  const apply = () => {
    const q = box.value.trim().toLowerCase();
    let shown = 0;
    for (const a of links) {
      const id = a.getAttribute("href").slice(1);
      const sec = document.getElementById(id);
      const hit = q === "" || a.textContent.toLowerCase().includes(q) || (sec && sec.textContent.toLowerCase().includes(q));
      a.hidden = !hit;
      if (sec && sec.classList.contains("ep")) sec.hidden = !hit;
      if (hit) shown++;
    }
    for (const g of groups) {
      let any = false;
      for (let n = g.nextElementSibling; n && !n.classList.contains("an-g"); n = n.nextElementSibling) {
        if (n.tagName === "A" && !n.hidden) any = true;
      }
      g.hidden = !any;
    }
    for (const s of sections) {
      if (s.tagName !== "H2") continue;
      let any = false;
      for (let n = s.nextElementSibling; n && !n.classList.contains("apigroup"); n = n.nextElementSibling) {
        if (n.classList.contains("ep") && !n.hidden) any = true;
      }
      s.hidden = !any;
    }
    if (q === "") {
      count.hidden = true;
      count.textContent = "";
      return;
    }
    count.hidden = false;
    count.textContent = t("%d of %d endpoints", shown, links.length);
  };
  box.addEventListener("input", apply);
})();

// A page that refreshes itself (the board's reload pill, the deliveries list,
// the relative times) is confusing when the network is gone: everything quietly
// stops. Say so, and say when it is back.
(() => {
  if (!("onLine" in navigator)) return;
  const bar = document.createElement("div");
  bar.className = "offline-bar";
  bar.setAttribute("role", "status");
  bar.setAttribute("aria-live", "polite");
  bar.hidden = true;
  bar.textContent = t("You are offline: this page cannot refresh until the connection is back.");
  document.body.append(bar);
  const sync = () => { bar.hidden = navigator.onLine; };
  addEventListener("online", sync);
  addEventListener("offline", sync);
  sync();
})();

// Background checks (the poll pill, the reload hint, the deliveries list) fail
// silently when the instance is restarting or unreachable: the page looks fine
// and simply stops updating. Two failures in a row raise the same bar the
// offline notice uses; one success clears it.
(() => {
  let misses = 0;
  const bar = () => document.querySelector(".offline-bar");
  const show = (text) => {
    const b = bar();
    if (!b) return;
    b.textContent = text;
    b.hidden = false;
  };
  const clear = () => {
    const b = bar();
    if (!b) return;
    b.textContent = t("You are offline: this page cannot refresh until the connection is back.");
    b.hidden = navigator.onLine;
  };
  window.hdConn = {
    ok() {
      misses = 0;
      clear();
    },
    fail() {
      misses++;
      if (misses < 2) return;
      show(t("Cannot reach HuntDeck: the last few checks failed. Is it restarting?"));
    },
  };
})();

// The search box's tooltip says "? for every shortcut" and nothing answered it:
// a promise in the interface with no feature behind it. This is the list, and it
// only names keys that exist.
(() => {
	const rows = [
		["/", t("Focus the search box")],
		["Esc", t("Close a menu, the mobile navigation or a dialog")],
		["g then a letter", t("Open a page: h board, q queue, p programs, t targets, c changes, d disclosed, s settings, a API")],
		["?", t("This list")],
	];
	const dlg = document.createElement("dialog");
	dlg.className = "keyhelp";
	dlg.innerHTML = "<h2>" + t("Keyboard") + "</h2><dl>" +
		rows.map(([k, d]) => "<dt><kbd>" + k + "</kbd></dt><dd>" + d + "</dd>").join("") +
		"</dl><form method=\"dialog\"><button class=\"button small\">" + t("Close") + "</button></form>";
	document.body.append(dlg);
	document.addEventListener("keydown", (e) => {
		if (e.key !== "?" || e.ctrlKey || e.metaKey || e.altKey) return;
		const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
		if (tag === "input" || tag === "textarea" || tag === "select") return;
		e.preventDefault();
		if (!dlg.open) dlg.showModal();
	});
})();

// g then a letter opens a page, as the keyboard section of the docs has said
// for a while without the handler existing. The window is short enough that a
// stray g while typing is not a navigation.
(() => {
	const pages = {
		h: "/", q: "/queue", p: "/programs", t: "/targets",
		c: "/changes", d: "/disclosed", s: "/settings", a: "/api",
	};
	let waiting = 0;
	document.addEventListener("keydown", (e) => {
		if (e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
		const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
		if (tag === "input" || tag === "textarea" || tag === "select") return;
		if (document.querySelector("dialog[open]")) return;
		if (e.key === "g") {
			waiting = Date.now();
			return;
		}
		if (!waiting || Date.now() - waiting > 800) return;
		const to = pages[e.key.toLowerCase()];
		waiting = 0;
		if (!to) return;
		e.preventDefault();
		location.assign(to);
	});
})();

// Settings › Display: the text size, kept in this browser and applied to the
// shell (--fs-zoom), so every page scales without a second set of breakpoints.
(() => {
	const group = document.querySelector("[data-fs-group]");
	if (!group) return;
	const steps = ["sm", "md", "lg", "xl"];
	const apply = (v) => {
		if (v === "md") delete document.documentElement.dataset.fs;
		else document.documentElement.dataset.fs = v;
		for (const b of group.querySelectorAll("button")) {
			b.setAttribute("aria-checked", b.dataset.fs === v ? "true" : "false");
		}
	};
	apply(localStorage.getItem("huntdeck-fs") || "md");
	group.addEventListener("click", (e) => {
		const b = e.target.closest("button[data-fs]");
		if (!b || !steps.includes(b.dataset.fs)) return;
		localStorage.setItem("huntdeck-fs", b.dataset.fs);
		apply(b.dataset.fs);
	});
})();


// A title is readable by a mouse and mostly invisible to a keyboard: focus a
// button and the hint never appears. This shows the same text under whatever
// takes focus, clamped to the window, and hides it again on blur, scroll or
// Escape. The title stays where it is, so screen readers keep reading it.
(() => {
	let tip = null;
	let owner = null;
	const hide = () => {
		if (tip) tip.remove();
		tip = null;
		owner = null;
	};
	const show = (el) => {
		const text = el.getAttribute("title");
		if (!text) return;
		hide();
		tip = document.createElement("div");
		tip.className = "focus-tip";
		tip.setAttribute("role", "tooltip");
		tip.textContent = text;
		document.body.append(tip);
		owner = el;
		const r = el.getBoundingClientRect();
		const w = tip.offsetWidth;
		const left = Math.min(Math.max(8, r.left + r.width / 2 - w / 2), innerWidth - w - 8);
		let top = r.bottom + 8;
		if (top + tip.offsetHeight > innerHeight - 8) top = Math.max(8, r.top - tip.offsetHeight - 8);
		tip.style.left = left + "px";
		tip.style.top = top + "px";
	};
	document.addEventListener("focusin", (e) => {
		const el = e.target;
		if (el instanceof Element && el.hasAttribute("title")) show(el);
		else hide();
	});
	document.addEventListener("focusout", hide);
	document.addEventListener("keydown", (e) => { if (e.key === "Escape") hide(); });
	addEventListener("scroll", hide, { passive: true });
})();

// The sidebar's fold button: icons only, or icons and names; remembered
// (theme.js applies it before the page shows). Folded, each icon's name
// shows on hover.
(() => {
  const btn = document.querySelector("[data-side-toggle]");
  if (!btn) return;
  const root = document.documentElement;
  for (const a of document.querySelectorAll(".nav a")) {
    const label = a.querySelector(".label");
    if (label && !a.title) a.title = label.textContent.trim();
  }
  const show = () => {
    const min = root.dataset.side === "min";
    btn.setAttribute("aria-pressed", String(min));
    btn.title = min ? t("Show the sidebar's names") : t("Fold the sidebar to its icons");
    btn.setAttribute("aria-label", min ? t("Unfold the sidebar") : t("Fold the sidebar"));
  };
  show();
  btn.addEventListener("click", () => {
    if (root.dataset.side === "min") delete root.dataset.side;
    else root.dataset.side = "min";
    try {
      if (root.dataset.side === "min") localStorage.setItem("huntdeck-side", "min");
      else localStorage.removeItem("huntdeck-side");
    } catch { /* storage blocked: folded for this page only */ }
    show();
  });
})();

// Collapsible sidebar sections (System, Integrations): server renders active
// section open and others collapsed; manual toggles persist in localStorage.
(() => {
  const KEY_PREFIX = "huntdeck-nav-section:";
  const read = (name) => {
    try { return localStorage.getItem(KEY_PREFIX + name); } catch { return null; }
  };
  const write = (name, val) => {
    try { localStorage.setItem(KEY_PREFIX + name, val); } catch { /* ignore */ }
  };
  for (const btn of document.querySelectorAll("[data-nav-toggle-section]")) {
    const name = btn.dataset.navToggleSection;
    const sec = document.querySelector(`[data-nav-section="${name}"]`);
    if (!sec) continue;
    const head = btn.closest(".nav-group-head");
    const hasActive = Boolean(
      sec.querySelector('[aria-current="page"]') ||
      head?.querySelector('[aria-current="page"]')
    );
    const sync = (open) => {
      sec.hidden = !open;
      btn.setAttribute("aria-expanded", String(open));
    };
    if (hasActive) {
      sync(true);
    } else {
      const saved = read(name);
      if (saved === "open") sync(true);
      else if (saved === "closed") sync(false);
    }
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const next = sec.hidden;
      sync(next);
      write(name, next ? "open" : "closed");
    });
  }
})();
