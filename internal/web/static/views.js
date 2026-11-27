// Saved views (myviews.go): a page's filters, sort and columns under a
// name of the user's, kept in this browser (localStorage, as the
// watchlist and notes are), never sent anywhere.
// - Save view asks for a name and keeps the page's query as it is now
//   (the keys the page reads, data-view-keys) and, for a table with a
//   column picker (data-view-cols), its columns as columns.js keeps them.
// - My views lists the page's own views: each loads its query (and puts
//   its columns back), or can be renamed or deleted. v opens the menu.
// - A view saved before a key went away loads without it: only the keys
//   the page reads now are kept.
// - Export views and Import views move every page's views as JSON; an
//   imported view replaces one of the same name only if it's newer.
(() => {
  const KEY = "huntdeck.views", FORMAT = "huntdeck-views", NAME_MAX = 60, PER_PAGE = 100;
  const init = (_root, signal) => {
  const box = document.querySelector("[data-views]");
  if (!box) return;
  const page = box.dataset.views;
  const keys = new Set((box.dataset.viewKeys || "").split(",").filter(Boolean));
  const cols = box.dataset.viewCols || "";
  const colKeys = cols ? [`huntdeck-${cols}-columns-v2`, `huntdeck-${cols}-column-order`] : [];
  const list = box.querySelector("[data-view-list]");
  const count = box.querySelector("[data-view-count]");
  const menu = box.querySelector("details");

  const read = () => {
    try {
      const v = JSON.parse(localStorage.getItem(KEY));
      return v && typeof v === "object" && !Array.isArray(v) ? v : {};
    } catch { return {}; }
  };
  const write = (all) => {
    try { localStorage.setItem(KEY, JSON.stringify(all)); return true; } catch { return false; }
  };
  // A stored view, checked: anything malformed is left out, never fatal.
  const valid = (v) => v && typeof v === "object" && typeof v.name === "string" && v.name.trim() !== "" &&
    typeof v.query === "string" && v.query.length < 4000;
  const viewsOf = (all, p) => (Array.isArray(all[p]) ? all[p].filter(valid) : []);

  // The query as the page reads it: its own keys, in a stable order.
  const clean = (search) => {
    const q = new URLSearchParams(search);
    const out = new URLSearchParams();
    for (const [k, v] of [...q].sort(([a], [b]) => a.localeCompare(b))) {
      if (keys.has(k) && v !== "") out.append(k, v);
    }
    return out.toString();
  };
  const here = () => clean(location.search);
  const href = (v) => {
    const q = clean(v.query);
    return page + (q ? "?" + q : "");
  };

  // A small dialog asking for a name, or a yes; null if cancelled.
  let dlg = document.querySelector("dialog.vdialog:not(.ask)");
  const ask = ({ title, text = "", value = null, ok }) => new Promise((resolve) => {
    if (!dlg) {
      dlg = document.createElement("dialog");
      dlg.className = "vdialog";
      dlg.innerHTML = `<form method="dialog"><h2></h2><p class="muted small"></p>
        <input type="text" maxlength="${NAME_MAX}" autocomplete="off" spellcheck="false" aria-label="${t("View name")}">
        <div class="vdialog-acts"><button type="button" class="button secondary small" data-cancel>${t("Cancel")}</button>
        <button type="submit" value="ok" class="button small"></button></div></form>`;
      document.body.append(dlg);
      dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close("cancel"); }); // the backdrop
      // Cancel isn't a submit button, so Enter in the name saves.
      dlg.querySelector("[data-cancel]").addEventListener("click", () => dlg.close("cancel"));
    }
    const input = dlg.querySelector("input");
    dlg.querySelector("h2").textContent = title;
    dlg.querySelector("p").textContent = text;
    dlg.querySelector("p").hidden = !text;
    dlg.querySelector('[value="ok"]').textContent = ok;
    input.hidden = value === null;
    input.required = value !== null;
    input.value = value ?? "";
    dlg.returnValue = "";
    dlg.onclose = () => {
      if (dlg.returnValue !== "ok") return resolve(null);
      resolve(value === null ? true : input.value.trim().slice(0, NAME_MAX) || null);
    };
    dlg.showModal();
    if (value !== null) input.select();
  });

  const say = (msg) => {
    let s = box.querySelector(".vstatus");
    if (!s) {
      s = document.createElement("span");
      s.className = "vstatus";
      s.setAttribute("role", "status");
      box.append(s);
    }
    s.textContent = msg;
    clearTimeout(s.timer);
    s.timer = setTimeout(() => { s.textContent = ""; }, 4000);
  };

  // The menu's list, from the views as stored now.
  const render = () => {
    const views = viewsOf(read(), page);
    count.textContent = views.length ? String(views.length) : "";
    list.replaceChildren();
    if (!views.length) {
      const p = document.createElement("p");
      p.className = "menu-note";
      p.textContent = t("No saved views yet. Set your filters and hit Save view.");
      list.append(p);
      return;
    }
    const now = here();
    for (const v of views) {
      const row = document.createElement("div");
      row.className = "vrow";
      const a = document.createElement("a");
      a.href = href(v);
      a.textContent = v.name;
      a.title = v.name + "\nLoad: " + (clean(v.query) || "the page's defaults") + (v.cols ? ", with its columns" : "");
      if (clean(v.query) === now) a.setAttribute("aria-current", "true");
      a.addEventListener("click", () => putColumns(v));
      const btn = (label, cls, act) => {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "vact " + cls;
        b.setAttribute("aria-label", label + " " + v.name);
        b.title = label;
        b.addEventListener("click", act);
        return b;
      };
      row.append(a, btn(t("Rename"), "rename", () => rename(v.name)), btn(t("Delete"), "delete", () => remove(v.name)));
      list.append(row);
    }
  };

  const columnsNow = () => {
    if (!colKeys.length) return null;
    const out = {};
    for (const k of colKeys) {
      try { const v = localStorage.getItem(k); if (v !== null) out[k] = v; } catch { /* storage off */ }
    }
    return out;
  };
  const putColumns = (v) => {
    if (!colKeys.length || !v.cols || typeof v.cols !== "object") return;
    try {
      for (const k of colKeys) {
        if (typeof v.cols[k] === "string") localStorage.setItem(k, v.cols[k]);
        else localStorage.removeItem(k); // the defaults, as when it was saved
      }
    } catch { /* storage off: the filters still load */ }
  };

  const update = (fn) => {
    const all = read();
    const views = viewsOf(all, page);
    const out = fn(views);
    if (out === null) return false;
    all[page] = out;
    if (!write(all)) {
      say(t("Not saved: this browser's storage is full or turned off"));
      return false;
    }
    render();
    return true;
  };
  const same = (a, b) => a.trim().toLowerCase() === b.trim().toLowerCase();

  box.querySelector("[data-view-save]").addEventListener("click", async () => {
    const name = await ask({ title: t("Save view"), text: t("These filters, sort and columns, kept in this browser. A view with the same name is replaced."), value: "", ok: t("Save") });
    if (!name) return;
    const v = { name, query: here(), cols: columnsNow(), saved: new Date().toISOString() };
    if (update((views) => {
      const rest = views.filter((x) => !same(x.name, name));
      if (rest.length >= PER_PAGE) { say(`At most ${PER_PAGE} views a page: delete one first`); return null; }
      return [...rest, v].sort((a, b) => a.name.localeCompare(b.name));
    })) say(`Saved “${name}”`);
  });
  const rename = async (old) => {
    menu.open = false;
    const name = await ask({ title: t("Rename view"), value: old, ok: t("Rename") });
    if (!name || name === old) return;
    if (update((views) => {
      const rest = views.filter((x) => same(x.name, old) || !same(x.name, name)); // a view already called that is replaced
      return rest.map((x) => (same(x.name, old) ? { ...x, name } : x)).sort((a, b) => a.name.localeCompare(b.name));
    })) say(`Renamed to “${name}”`);
  };
  const remove = async (name) => {
    menu.open = false;
    if (!(await ask({ title: t("Delete view"), text: t("Delete “%s”? Its filters stay as they are on the page.", name), ok: t("Delete") }))) return;
    if (update((views) => views.filter((x) => !same(x.name, name)))) say(`Deleted “${name}”`);
  };

  box.querySelector("[data-view-export]").addEventListener("click", () => {
    const all = read();
    const views = {};
    for (const p of Object.keys(all).sort()) {
      const vs = viewsOf(all, p);
      if (p.startsWith("/") && vs.length) views[p] = vs;
    }
    const n = Object.values(views).reduce((s, vs) => s + vs.length, 0);
    const blob = new Blob([JSON.stringify({ format: FORMAT, version: 1, exported: new Date().toISOString(), views }, null, 2) + "\n"],
      { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "huntdeck-views-" + new Date().toISOString().slice(0, 10) + ".json";
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    say(`Exported ${n} ${n === 1 ? "view" : "views"}`);
  });
  const file = box.querySelector("[data-view-file]");
  box.querySelector("[data-view-import]").addEventListener("click", () => file.click());
  file.addEventListener("change", async () => {
    const f = file.files?.[0];
    file.value = "";
    if (!f) return;
    let doc;
    try {
      if (f.size > 5 * 1024 * 1024) throw new Error("too big");
      doc = JSON.parse(await f.text());
      if (doc?.format !== FORMAT || !doc.views || typeof doc.views !== "object") throw new Error("not a views file");
    } catch {
      say(t("Not imported: that isn't a HuntDeck views export"));
      return;
    }
    const all = read();
    let added = 0, kept = 0, bad = 0;
    for (const [p, vs] of Object.entries(doc.views)) {
      if (!p.startsWith("/") || !Array.isArray(vs)) { bad++; continue; }
      const mine = viewsOf(all, p);
      for (const v of vs) {
        if (!valid(v)) { bad++; continue; }
        const theirs = { name: v.name.trim().slice(0, NAME_MAX), query: v.query,
          cols: v.cols && typeof v.cols === "object" ? v.cols : null,
          saved: typeof v.saved === "string" && !isNaN(Date.parse(v.saved)) ? v.saved : new Date(0).toISOString() };
        const i = mine.findIndex((x) => same(x.name, theirs.name));
        if (i >= 0 && Date.parse(mine[i].saved || 0) >= Date.parse(theirs.saved)) { kept++; continue; }
        if (i >= 0) mine[i] = theirs;
        else if (mine.length < PER_PAGE) mine.push(theirs);
        else { bad++; continue; }
        added++;
      }
      all[p] = mine.sort((a, b) => a.name.localeCompare(b.name));
    }
    if (!write(all)) { say(t("Not imported: this browser's storage is full or turned off")); return; }
    render();
    say(`Imported ${added}${kept ? `, kept ${kept} newer here` : ""}${bad ? `, skipped ${bad}` : ""}`);
  });

  window.addEventListener("storage", (e) => { if (e.key === KEY) render(); }, { signal });
  render();
  box.hidden = false;
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init(document, new AbortController().signal);
})();
