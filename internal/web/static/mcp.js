// MCP Developer Portal (mcp.html): interactive client config tabs, live tool
// directory filtering, and expand/collapse controls. Zero inline scripts.
(() => {
  // 1. Client config console tabs
  const consoleEl = document.querySelector("[data-mcp-console]");
  if (consoleEl) {
    const tabs = [...consoleEl.querySelectorAll("[data-mcp-tab]")];
    const panels = [...consoleEl.querySelectorAll("[data-mcp-panel]")];
    const copyBtn = consoleEl.querySelector("[data-mcp-active-copy]");
    const activate = (id) => {
      for (const tab of tabs) {
        const on = tab.dataset.mcpTab === id;
        tab.setAttribute("aria-selected", String(on));
        tab.tabIndex = on ? 0 : -1;
        if (on && copyBtn && tab.dataset.mcpCopy) {
          copyBtn.dataset.copy = tab.dataset.mcpCopy;
        }
      }
      for (const panel of panels) {
        panel.hidden = panel.dataset.mcpPanel !== id;
      }
    };
    for (const tab of tabs) {
      tab.addEventListener("click", () => activate(tab.dataset.mcpTab));
      tab.addEventListener("keydown", (e) => {
        const idx = tabs.indexOf(tab);
        if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
          e.preventDefault();
          const dir = e.key === "ArrowRight" ? 1 : -1;
          const next = tabs[(idx + dir + tabs.length) % tabs.length];
          activate(next.dataset.mcpTab);
          next.focus();
        }
      });
    }
    if (tabs.length) activate(tabs[0].dataset.mcpTab);
  }

  // 2. Live tool directory search filter (syncs left nav + main tool cards)
  const filterInput = document.querySelector("input[data-filter-mcp]");
  if (filterInput) {
    const nav = filterInput.closest(".mcp-nav");
    const directory = nav.querySelector("details");
    const compact = matchMedia("(max-width: 900px)");
    const fitDirectory = () => { if (directory) directory.open = !compact.matches; };
    fitDirectory();
    compact.addEventListener("change", fitDirectory);

    const countEl = nav.querySelector(".filter-count");
    const navLinks = [...nav.querySelectorAll("[data-mcp-nav-link]")];
    const navGroups = [...nav.querySelectorAll("[data-mcp-nav-group]")];
    const groupHeads = [...document.querySelectorAll("[data-mcp-group-head]")];
    const cards = [...document.querySelectorAll("[data-mcp-tool]")];
    const emptyEl = document.querySelector("[data-mcp-empty]");

    const applyFilter = () => {
      const q = filterInput.value.trim().toLowerCase();
      let shown = 0;
      const visibleByGroup = new Set();

      for (const card of cards) {
        const name = card.dataset.mcpTool;
        const navLink = nav.querySelector(`[data-mcp-nav-link="${CSS.escape(name)}"]`);
        const hit = q === "" ||
          name.toLowerCase().includes(q) ||
          card.textContent.toLowerCase().includes(q);
        card.hidden = !hit;
        if (navLink) navLink.hidden = !hit;
        if (hit) {
          shown++;
          if (card.dataset.mcpGroup) visibleByGroup.add(card.dataset.mcpGroup);
        }
      }

      for (const g of navGroups) {
        g.hidden = !visibleByGroup.has(g.dataset.mcpNavGroup);
      }
      for (const h of groupHeads) {
        h.hidden = !visibleByGroup.has(h.dataset.mcpGroupHead);
      }
      if (emptyEl) {
        emptyEl.hidden = shown > 0;
      }

      if (!countEl) return;
      if (q === "") {
        countEl.hidden = true;
        countEl.textContent = "";
      } else {
        countEl.hidden = false;
        countEl.textContent = t("%d of %d tools", shown, navLinks.length);
      }
    };

    filterInput.addEventListener("input", applyFilter);
  }

  // 3. Expand all / Collapse all tool cards + auto-open on anchor jump
  const toggleAllBtn = document.querySelector("[data-mcp-toggle-all]");
  const disclosures = [...document.querySelectorAll(".mcp-tool-disclosure")];
  const syncToggleLabel = () => {
    if (!toggleAllBtn) return;
    const anyClosed = disclosures.some((d) => !d.open);
    toggleAllBtn.dataset.state = anyClosed ? "collapsed" : "expanded";
    toggleAllBtn.textContent = anyClosed ? t("Expand all") : t("Collapse all");
  };
  const openTargetDisclosure = (hash) => {
    if (!hash || hash.length < 2) return;
    const target = document.getElementById(decodeURIComponent(hash.slice(1)));
    const disc = target && (target.matches(".mcp-tool-disclosure") ? target : target.querySelector(".mcp-tool-disclosure"));
    if (disc && !disc.open) {
      disc.open = true;
      syncToggleLabel();
    }
  };
  window.addEventListener("hashchange", () => openTargetDisclosure(location.hash));
  document.addEventListener("click", (e) => {
    const a = e.target.closest("[data-mcp-nav-link]");
    if (a && a.hash) openTargetDisclosure(a.hash);
  });
  openTargetDisclosure(location.hash);

  if (toggleAllBtn && disclosures.length > 0) {
    toggleAllBtn.hidden = false;
    syncToggleLabel();
    toggleAllBtn.addEventListener("click", () => {
      const openAll = toggleAllBtn.dataset.state === "collapsed";
      for (const d of disclosures) d.open = openAll;
      syncToggleLabel();
    });
    for (const d of disclosures) {
      d.addEventListener("toggle", syncToggleLabel);
    }
  }
})();
