// Comboboxes: a text box ([data-combo-input]) inside [data-combo] that
// lists the options of its [data-combo-list] matching what's typed. Every
// word typed must appear in an option's data-search, and whole-word and
// word-start matches come first; arrows move through
// the matches, Enter or a click picks one (its data-value fills the box),
// Escape closes. Opening without typing shows them all, the current one in
// view. A [data-combo-fill-tz="input id"] button offers the browser's own
// time zone where that's one of the input's options.
(() => {
  const norm = (s) => s.toLowerCase().replace(/[\/_]/g, " ").replace(/−/g, "-").trim();
  const init = () => {
  document.querySelectorAll("[data-combo]").forEach((combo) => {
    if (combo.dataset.comboReady) return;
    combo.dataset.comboReady = "1";
    const input = combo.querySelector("[data-combo-input]");
    const list = combo.querySelector("[data-combo-list]");
    const toggle = combo.querySelector("[data-combo-toggle]");
    const empty = list.querySelector(".combo-empty");
    const options = [...list.querySelectorAll("[role=option]")];
    let active = null;
    let picked = input.value;

    const shown = () => [...list.querySelectorAll("[role=option]:not([hidden])")];
    const setActive = (o, scroll) => {
      if (active) active.classList.remove("active");
      active = o;
      if (o) {
        o.classList.add("active");
        input.setAttribute("aria-activedescendant", o.id);
        if (scroll) o.scrollIntoView({ block: "nearest" });
      } else input.removeAttribute("aria-activedescendant");
    };
    const tokens = new Map(options.map((o) => [o, o.dataset.search.split(/[\s()]+/)]));
    // How well word w matches o: a whole word (an abbreviation like "ist")
    // beats the start of one ("syd"), which beats anywhere in one.
    const rank = (o, w) => {
      const t = tokens.get(o);
      if (t.includes(w)) return 0;
      if (t.some((x) => x.startsWith(w))) return 1;
      return o.dataset.search.includes(w) ? 2 : -1;
    };
    // Shows the options matching every word typed, best matches first and
    // otherwise in their own order.
    const filter = (q) => {
      const words = norm(q).split(/\s+/).filter(Boolean);
      const scored = options.map((o, i) => {
        let score = 0;
        for (const w of words) {
          const r = rank(o, w);
          if (r < 0) return { o, i, score: -1 };
          score += r;
        }
        return { o, i, score };
      });
      for (const s of scored) s.o.hidden = s.score < 0;
      scored.filter((s) => s.score >= 0).sort((a, b) => a.score - b.score || a.i - b.i)
        .forEach((s) => list.insertBefore(s.o, empty));
      empty.hidden = shown().length > 0;
      setActive(shown()[0] || null, true);
    };
    const open = (all) => {
      if (all) filter("");
      list.hidden = false;
      input.setAttribute("aria-expanded", "true");
      if (all) {
        const cur = options.find((o) => o.dataset.value === picked);
        setActive(cur || options[0], false);
        if (cur) list.scrollTop = cur.offsetTop - list.clientHeight / 2 + cur.offsetHeight / 2;
      }
    };
    const close = () => {
      list.hidden = true;
      input.setAttribute("aria-expanded", "false");
      setActive(null);
    };
    const pick = (o) => {
      picked = input.value = o.dataset.value;
      options.forEach((x) => x.setAttribute("aria-selected", String(x === o)));
      close();
      input.dispatchEvent(new Event("change", { bubbles: true }));
    };

    input.addEventListener("focus", () => { input.select(); open(true); });
    input.addEventListener("click", () => { if (list.hidden) open(true); });
    input.addEventListener("input", () => { open(false); filter(input.value); });
    input.addEventListener("blur", () => {
      close();
      if (!input.value.trim()) input.value = picked; // left empty: keep the last choice
    });
    input.addEventListener("keydown", (e) => {
      const s = shown();
      const i = s.indexOf(active);
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          if (list.hidden) open(true); else setActive(s[Math.min(i + 1, s.length - 1)] || null, true);
          break;
        case "ArrowUp":
          e.preventDefault();
          if (!list.hidden) setActive(s[Math.max(i - 1, 0)] || null, true);
          break;
        case "Enter":
          if (!list.hidden && active) { e.preventDefault(); pick(active); }
          break;
        case "Escape":
          if (!list.hidden) { e.preventDefault(); input.value = picked; close(); }
          break;
      }
    });
    // Picking with the mouse mustn't blur the box first.
    list.addEventListener("mousedown", (e) => e.preventDefault());
    list.addEventListener("click", (e) => {
      const o = e.target.closest("[role=option]");
      if (o) pick(o);
    });
    list.addEventListener("mousemove", (e) => {
      const o = e.target.closest("[role=option]");
      if (o && o !== active) setActive(o, false);
    });
    toggle?.addEventListener("mousedown", (e) => {
      e.preventDefault();
      if (list.hidden) { input.focus(); open(true); } else close();
    });
  });

  // "Use this browser's": the browser's time zone, if it's on the list.
  document.querySelectorAll("[data-combo-fill-tz]").forEach((btn) => {
    if (btn.dataset.tzReady) return;
    btn.dataset.tzReady = "1";
    const input = document.getElementById(btn.dataset.comboFillTz);
    let tz = "";
    try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch { /* no Intl */ }
    const list = input && document.getElementById(input.getAttribute("aria-controls"));
    if (!tz || !list || tz === input.value || !list.querySelector(`[data-value="${CSS.escape(tz)}"]`)) return;
    btn.querySelector("b").textContent = tz;
    btn.hidden = false;
    btn.addEventListener("click", () => {
      list.querySelector(`[data-value="${CSS.escape(tz)}"]`).click();
      btn.hidden = true;
    });
  });
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init();
})();
