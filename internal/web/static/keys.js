// Keyboard shortcuts, never while typing in a box: "?" lists them.
// - j and k move through a list's rows; Enter or o opens the row (in a new
//   tab with Shift); x ticks its checkbox.
// - g then a letter goes to a page.
// - "/" (app.js) focuses the search.
(() => {
  const pages = [
    ["h", "/", "Hunt board"], ["p", "/programs", "Programs"], ["i", "/invites", "Invites"], ["t", "/targets", "Targets"],
    ["c", "/changes", "Changes"], ["d", "/disclosed", "Disclosed"], ["s", "/stats", "Stats"], ["m", "/match", "Scope check"],
  ];
  const typing = (t) => t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName);
  const rows = () => [...document.querySelectorAll("main tr[data-href], main .nrow[data-href]")].filter((r) => r.offsetParent !== null);
  let cur = null; // the row j and k are on
  let g = 0; // when "g" was pressed, waiting for the page's letter

  const mark = (row) => {
    cur?.classList.remove("kb-row");
    cur = row;
    if (!row) return;
    row.classList.add("kb-row");
    row.scrollIntoView({ block: "nearest" });
  };
  const open = (row, newTab) => {
    const url = row.dataset.href;
    if ("ext" in row.dataset || newTab) window.open(url, "_blank", "noopener,noreferrer");
    else location.href = url;
  };

  let help;
  const showHelp = () => {
    if (!help) {
      help = document.createElement("dialog");
      help.className = "kb-help";
      help.setAttribute("aria-label", t("Keyboard shortcuts"));
      const keys = [["/", t("Search")], ["j / k", t("Next / previous row")], ["Enter or o", t("Open the row (Shift: in a new tab)")],
        ["x", t("Tick the row's checkbox")], ["n", t("A program's notes (on its page)")], ["v", t("Your saved views (Hunt board, Programs, Targets)")], ...pages.map(([k, , name]) => ["g " + k, t(name)]), ["?", t("These shortcuts")], ["Esc", t("Close")]];
      const h = document.createElement("h2");
      h.textContent = t("Keyboard shortcuts");
      const dl = document.createElement("dl");
      for (const [k, what] of keys) {
        const dt = document.createElement("dt");
        for (const part of k.split(" ")) {
          if (part === "/" && k !== "/") { dt.append(" / "); continue; }
          if (part === "or") { dt.append(" or "); continue; }
          const kbd = document.createElement("kbd");
          kbd.textContent = part;
          dt.append(kbd, " ");
        }
        const dd = document.createElement("dd");
        dd.textContent = what;
        dl.append(dt, dd);
      }
      const close = document.createElement("button");
      close.type = "button";
      close.className = "button secondary small";
      close.textContent = t("Close");
      close.addEventListener("click", () => help.close());
      help.addEventListener("click", (e) => { if (e.target === help) help.close(); }); // the backdrop
      help.append(h, dl, close);
      document.body.append(help);
    }
    help.showModal();
  };

  document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented || typing(e.target) || help?.open) return;
    if (g && Date.now() - g < 1500) {
      g = 0;
      const page = pages.find(([k]) => k === e.key);
      if (page) { e.preventDefault(); location.href = page[1]; }
      return;
    }
    g = 0;
    switch (e.key) {
      case "?":
        e.preventDefault();
        showHelp();
        return;
      case "g":
        g = Date.now();
        return;
      case "v": { // the page's saved views
        const menu = document.querySelector("[data-views]:not([hidden]) details");
        if (!menu) return;
        e.preventDefault();
        menu.open = !menu.open;
        if (menu.open) (menu.querySelector(".vrow a") || menu.querySelector("button"))?.focus();
        return;
      }
      case "n": { // a program's notes
        const tab = document.querySelector("[data-notes-tab]");
        if (!tab || tab.getAttribute("aria-current")) return;
        e.preventDefault();
        location.href = tab.href;
        return;
      }
      case "j":
      case "k": {
        const list = rows();
        if (!list.length) return;
        e.preventDefault();
        const i = list.indexOf(cur);
        const next = i < 0 ? (e.key === "j" ? 0 : list.length - 1) : Math.min(list.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)));
        mark(list[next]);
        return;
      }
      case "Enter":
      case "o":
      case "O":
        // Enter on a focused link or button is its own.
        if (!cur || !cur.isConnected || (e.key === "Enter" && e.target !== document.body)) return;
        e.preventDefault();
        open(cur, e.shiftKey);
        return;
      case "x": {
        const box = cur?.isConnected && cur.querySelector("input[type=checkbox]");
        if (!box) return;
        e.preventDefault();
        box.click();
        return;
      }
      case "Escape":
        mark(null);
    }
  });
})();
