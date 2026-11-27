// Dual-month date range picker for Reports > Analytics (.arange[data-drp]).
// Progressive enhancement: the form works without JS; with JS, clicking the
// unified pill opens a floating dual-month calendar popover, supports hover
// range preview, auto-swaps inverted clicks, and submits on completion.
(() => {
  const tr = (msg) => (typeof window.t === "function" ? window.t(msg) : msg);
  const isZh = (document.documentElement.lang || "").toLowerCase().startsWith("zh");
  const MONTHS_EN = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const WEEK_EN = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];
  const WEEK_ZH = ["日", "一", "二", "三", "四", "五", "六"];

  const pad2 = (n) => (n < 10 ? "0" + n : "" + n);
  const keyOf = (d) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  const atMidnight = (y, m, d) => new Date(y, m, d);

  function parseDate(raw, endOfMonth) {
    const s = (raw || "").trim();
    let m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
    if (m) {
      const y = +m[1], mo = +m[2] - 1, d = +m[3];
      const dt = atMidnight(y, mo, d);
      if (dt.getFullYear() === y && dt.getMonth() === mo && dt.getDate() === d) return dt;
      return null;
    }
    m = /^(\d{4})-(\d{2})$/.exec(s);
    if (m) {
      const y = +m[1], mo = +m[2] - 1;
      if (mo < 0 || mo > 11) return null;
      return endOfMonth ? atMidnight(y, mo + 1, 0) : atMidnight(y, mo, 1);
    }
    return null;
  }

  function formatMonthTitle(year, month) {
    return isZh ? `${year} 年 ${month + 1} 月` : `${MONTHS_EN[month]} ${year}`;
  }

  document.querySelectorAll("form.arange[data-drp]").forEach((form) => {
    const pill = form.querySelector(".drp");
    const fromInput = form.querySelector('input[name="from"]');
    const toInput = form.querySelector('input[name="to"]');
    const clearBtn = form.querySelector(".drp-clear");
    if (!pill || !fromInput || !toInput) return;

    form.dataset.drpReady = "1";

    const today = new Date();
    const todayKey = keyOf(today);
    let viewYear = today.getFullYear();
    let viewMonth = today.getMonth();
    let pickStart = null; // Date while waiting for second click
    let hoverKey = null;
    let isOpen = false;
    let suppressFocusOpen = false;

    const pop = document.createElement("div");
    pop.className = "drp-pop";
    pop.hidden = true;
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-modal", "false");

    const caret = document.createElement("span");
    caret.className = "drp-caret";
    caret.setAttribute("aria-hidden", "true");
    pop.appendChild(caret);

    const panels = document.createElement("div");
    panels.className = "drp-panels";
    pop.appendChild(panels);

    function makeNavBtn(symbol, action, label, extraClass) {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "drp-nav" + (extraClass ? " " + extraClass : "");
      b.dataset.act = action;
      b.textContent = symbol;
      b.setAttribute("aria-label", label);
      b.title = label;
      return b;
    }

    function createCal(side) {
      const cal = document.createElement("div");
      cal.className = `drp-cal drp-cal--${side}`;

      const head = document.createElement("div");
      head.className = "drp-head";
      const title = document.createElement("span");
      title.className = "drp-title";

      if (side === "left") {
        head.appendChild(makeNavBtn("«", "prev-year", tr("Previous year")));
        head.appendChild(makeNavBtn("‹", "prev-month", tr("Previous month")));
        head.appendChild(title);
        head.appendChild(makeNavBtn("›", "next-month", tr("Next month"), "drp-nav-next"));
        head.appendChild(makeNavBtn("»", "next-year", tr("Next year"), "drp-nav-next"));
      } else {
        const sp1 = document.createElement("span");
        const sp2 = document.createElement("span");
        head.appendChild(sp1);
        head.appendChild(sp2);
        head.appendChild(title);
        head.appendChild(makeNavBtn("›", "next-month", tr("Next month")));
        head.appendChild(makeNavBtn("»", "next-year", tr("Next year")));
      }

      const wk = document.createElement("div");
      wk.className = "drp-wk";
      (isZh ? WEEK_ZH : WEEK_EN).forEach((name) => {
        const s = document.createElement("span");
        s.textContent = name;
        wk.appendChild(s);
      });

      const grid = document.createElement("div");
      grid.className = "drp-grid";

      cal.appendChild(head);
      cal.appendChild(wk);
      cal.appendChild(grid);
      return { cal, title, grid };
    }

    const leftCal = createCal("left");
    const rightCal = createCal("right");
    panels.appendChild(leftCal.cal);
    panels.appendChild(rightCal.cal);
    document.body.appendChild(pop);

    function syncClear() {
      if (!clearBtn) return;
      clearBtn.hidden = !(fromInput.value.trim() || toInput.value.trim());
    }

    function currentBounds() {
      if (pickStart) {
        const sk = keyOf(pickStart);
        const hk = hoverKey || sk;
        return hk < sk
          ? { start: hk, end: sk, preview: true }
          : { start: sk, end: hk, preview: true };
      }
      const f = parseDate(fromInput.value, false);
      const t = parseDate(toInput.value, true);
      if (f && t) {
        const fk = keyOf(f), tk = keyOf(t);
        return fk <= tk
          ? { start: fk, end: tk, preview: false }
          : { start: tk, end: fk, preview: false };
      }
      if (f) return { start: keyOf(f), end: keyOf(f), preview: false };
      if (t) return { start: keyOf(t), end: keyOf(t), preview: false };
      return { start: "", end: "", preview: false };
    }

    function applyClasses() {
      const { start, end, preview } = currentBounds();
      const single = start !== "" && start === end;
      pop.querySelectorAll(".drp-cell").forEach((cell) => {
        const k = cell.dataset.date;
        cell.classList.remove("is-start", "is-end", "is-in", "is-single", "is-preview", "is-preview-start", "is-preview-end", "is-preview-single");
        if (!start || !end) return;
        if (k === start) {
          cell.classList.add("is-start");
          if (single) cell.classList.add("is-single");
          if (preview) {
            cell.classList.add("is-preview-start");
            if (single) cell.classList.add("is-preview-single");
          }
        }
        if (k === end) {
          cell.classList.add("is-end");
          if (single) cell.classList.add("is-single");
          if (preview) {
            cell.classList.add("is-preview-end");
            if (single) cell.classList.add("is-preview-single");
          }
        }
        if (k > start && k < end) {
          cell.classList.add(preview ? "is-preview" : "is-in");
        }
      });
    }

    function renderPanel(panel, year, month) {
      panel.title.textContent = formatMonthTitle(year, month);
      panel.grid.textContent = "";
      const first = atMidnight(year, month, 1);
      const offset = first.getDay();
      const start = atMidnight(year, month, 1 - offset);
      const frag = document.createDocumentFragment();

      for (let i = 0; i < 42; i++) {
        const d = atMidnight(start.getFullYear(), start.getMonth(), start.getDate() + i);
        const k = keyOf(d);
        const out = d.getMonth() !== month;

        const cell = document.createElement("div");
        cell.className = "drp-cell";
        cell.dataset.date = k;
        if (out) cell.classList.add("is-out");
        if (k === todayKey) cell.classList.add("is-today");

        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "drp-day";
        btn.textContent = String(d.getDate());
        btn.dataset.date = k;
        btn.setAttribute("aria-label", k);
        cell.appendChild(btn);
        frag.appendChild(cell);
      }
      panel.grid.appendChild(frag);
    }

    function render() {
      const rightBase = new Date(viewYear, viewMonth + 1, 1);
      renderPanel(leftCal, viewYear, viewMonth);
      renderPanel(rightCal, rightBase.getFullYear(), rightBase.getMonth());
      applyClasses();
    }

    function position() {
      if (!isOpen) return;
      const rect = pill.getBoundingClientRect();
      const vw = document.documentElement.clientWidth;
      const vh = window.innerHeight;
      const margin = 8;

      pop.style.left = "0px";
      pop.style.top = "0px";
      const pw = pop.offsetWidth;
      const ph = pop.offsetHeight;

      let left = rect.left;
      if (left + pw > vw - margin) left = vw - pw - margin;
      if (left < margin) left = margin;

      let top = rect.bottom + 8;
      let placement = "bottom";
      if (top + ph > vh - margin && rect.top - ph - 8 >= margin) {
        top = rect.top - ph - 8;
        placement = "top";
      }

      const anchorCenter = rect.left + Math.min(rect.width * 0.5, 48);
      const caretX = Math.max(16, Math.min(pw - 26, Math.round(anchorCenter - left - 5)));

      pop.dataset.placement = placement;
      pop.style.left = Math.round(left) + "px";
      pop.style.top = Math.round(top) + "px";
      pop.style.setProperty("--drp-caret-x", caretX + "px");
    }

    function syncViewFromInputs() {
      const f = parseDate(fromInput.value, false);
      const t = parseDate(toInput.value, false);
      const anchor = f || t;
      if (anchor) {
        viewYear = anchor.getFullYear();
        viewMonth = anchor.getMonth();
      } else {
        viewYear = today.getFullYear();
        viewMonth = today.getMonth();
      }
    }

    function open() {
      if (isOpen) {
        position();
        return;
      }
      syncViewFromInputs();
      pickStart = null;
      hoverKey = null;
      isOpen = true;
      pill.classList.add("is-open");
      pop.hidden = false;
      render();
      position();
    }

    function close() {
      if (!isOpen) return;
      isOpen = false;
      pickStart = null;
      hoverKey = null;
      pill.classList.remove("is-open");
      pop.hidden = true;
    }

    function shiftMonth(delta) {
      const next = new Date(viewYear, viewMonth + delta, 1);
      viewYear = next.getFullYear();
      viewMonth = next.getMonth();
      render();
      position();
    }

    pop.addEventListener("mousedown", (e) => {
      if (e.target.closest("button")) e.preventDefault();
    });

    pop.addEventListener("click", (e) => {
      const nav = e.target.closest(".drp-nav");
      if (nav) {
        switch (nav.dataset.act) {
          case "prev-year": shiftMonth(-12); break;
          case "prev-month": shiftMonth(-1); break;
          case "next-month": shiftMonth(1); break;
          case "next-year": shiftMonth(12); break;
        }
        return;
      }
      const dayBtn = e.target.closest(".drp-day");
      if (!dayBtn) return;
      const clicked = parseDate(dayBtn.dataset.date, false);
      if (!clicked) return;

      if (!pickStart) {
        pickStart = clicked;
        hoverKey = keyOf(clicked);
        applyClasses();
        return;
      }

      let a = pickStart;
      let b = clicked;
      if (b < a) {
        const tmp = a;
        a = b;
        b = tmp;
      }
      fromInput.value = keyOf(a);
      toInput.value = keyOf(b);
      syncClear();
      close();
      if (typeof form.requestSubmit === "function") form.requestSubmit();
      else form.submit();
    });

    panels.addEventListener("mousemove", (e) => {
      if (!pickStart) return;
      const dayBtn = e.target.closest(".drp-day");
      if (!dayBtn) return;
      const k = dayBtn.dataset.date;
      if (k && k !== hoverKey) {
        hoverKey = k;
        applyClasses();
      }
    });

    panels.addEventListener("focusin", (e) => {
      if (!pickStart) return;
      const dayBtn = e.target.closest(".drp-day");
      if (!dayBtn) return;
      const k = dayBtn.dataset.date;
      if (k && k !== hoverKey) {
        hoverKey = k;
        applyClasses();
      }
    });

    panels.addEventListener("mouseleave", () => {
      if (!pickStart) return;
      const sk = keyOf(pickStart);
      if (hoverKey !== sk) {
        hoverKey = sk;
        applyClasses();
      }
    });

    pill.addEventListener("click", (e) => {
      if (e.target.closest(".drp-clear")) return;
      open();
      if (!e.target.closest("input")) fromInput.focus();
    });

    [fromInput, toInput].forEach((inp) => {
      inp.addEventListener("focus", () => {
        if (!suppressFocusOpen) open();
      });
      inp.addEventListener("input", () => {
        syncClear();
        if (isOpen) {
          syncViewFromInputs();
          render();
          position();
        }
      });
    });

    clearBtn?.addEventListener("click", (e) => {
      e.stopPropagation();
      const params = new URLSearchParams(window.location.search);
      const hadQueryRange = params.has("from") || params.has("to");
      fromInput.value = "";
      toInput.value = "";
      syncClear();
      close();
      if (hadQueryRange) {
        window.location.assign(form.getAttribute("action") || "/reports/analytics");
      }
    });

    document.addEventListener("mousedown", (e) => {
      if (!isOpen) return;
      if (!pill.contains(e.target) && !pop.contains(e.target)) close();
    });

    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && isOpen) {
        e.preventDefault();
        close();
        suppressFocusOpen = true;
        fromInput.focus();
        suppressFocusOpen = false;
      }
    });

    window.addEventListener("resize", position);
    window.addEventListener("scroll", position, { passive: true, capture: true });
    syncClear();
  });
})();
