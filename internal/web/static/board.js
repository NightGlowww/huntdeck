// Board layouts (the Hunt board's, Analytics'). A board is a 12-column
// grid: each box has a column, a width and, once resized, a height, and sits just under whatever is above it
// in those columns. In Customize mode a box can be dragged to another place
// (it snaps to the columns and the other boxes slide aside), resized by
// dragging its sides, bottom or bottom corner (widths in whole columns,
// heights in 20px steps; a box shorter than its content scrolls), or hidden.
// Layouts are saved in this browser: one for ultrawide screens and one for
// the rest, since a layout made for one rarely suits the other. Loaded right
// after the board, without defer, so the boxes are in place before the page
// first paints.
(() => {
  const init = (_root, signal) => {
  const board = document.querySelector("[data-board]");
  if (!board || board.dataset.boardReady) return;
  board.dataset.boardReady = "1";
  // Each board keeps its own layout: the Hunt board's under "huntdeck-board",
  // another's (data-board="analytics") under "huntdeck-board-analytics".
  const key = board.dataset.board ? "huntdeck-board-" + board.dataset.board : "huntdeck-board";
  const cols = 12;
  const minW = 2;
  const gap = 16; // as between panels (12-board.css)
  // data-board-even: boxes that start on the same row and fit their content
  // grow to the tallest of them, so a row ends level (Analytics); not a box
  // under 60% of that height, which would be left mostly empty.
  const even = board.hasAttribute("data-board-even");
  const step = 20; // height snap
  const minH = 120;
  const maxH = 4000;
  const boxes = [...board.querySelectorAll(":scope > [data-box]")];
  const byId = Object.fromEntries(boxes.map((b) => [b.dataset.box, b]));
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const snapH = (px) => clamp(Math.round(px / step) * step, minH, maxH);

  // Each box's content goes in a body that scrolls when the box is given a
  // height shorter than it.
  for (const b of boxes) {
    const body = document.createElement("div");
    body.className = "bx-body";
    body.append(...b.childNodes);
    b.append(body);
  }

  // mode picks the layout: "wide" for ultrawide screens, "std" for the rest,
  // and "stack" (one box per row, no customizing) below the supported sizes.
  const mode = () => (window.innerWidth >= 2800 ? "wide" : window.innerWidth <= 1300 ? "stack" : "std");
  // A place is [column, width, height]; a height of 0 fits the content.
  const defaults = (m) => ({
    order: boxes.map((b) => b.dataset.box),
    place: Object.fromEntries(boxes.map((b) => [b.dataset.box,
      [...(m === "wide" ? b.dataset.placeWide : b.dataset.place).split(" ").map(Number), 0]])),
    hidden: [],
  });
  // clean checks a saved layout against the boxes on the page: unknown boxes
  // are dropped, new ones go where the page puts them by default.
  const clean = (l, m) => {
    const d = defaults(m);
    if (!l || !Array.isArray(l.order)) return d;
    const order = [...new Set(l.order.filter((id) => byId[id]))];
    for (const id of d.order) if (!order.includes(id)) order.push(id);
    const place = {};
    for (const id of order) {
      const p = l.place && Array.isArray(l.place[id]) ? l.place[id].map(Math.round) : d.place[id];
      const w = clamp(p[1] || d.place[id][1], minW, cols);
      place[id] = [clamp(p[0] || 0, 0, cols - w), w, p[2] > 0 ? snapH(p[2]) : 0];
    }
    const hidden = Array.isArray(l.hidden) ? l.hidden.filter((id) => byId[id]) : [];
    return { order, place, hidden };
  };
  let saved = {};
  try {
    const s = JSON.parse(localStorage.getItem(key) || "null");
    if (s && s.v === 2 && s.layouts) saved = s.layouts;
  } catch (e) { /* ignore a broken saved layout */ }
  const layouts = { std: clean(saved.std, "std"), wide: clean(saved.wide, "wide") };
  const layout = () => layouts[mode() === "wide" ? "wide" : "std"];
  const save = () => {
    try { localStorage.setItem(key, JSON.stringify({ v: 2, layouts })); } catch (e) { /* ignore */ }
  };

  const slot = document.createElement("div");
  slot.className = "bx-slot";
  let editing = false;
  let drag = null;

  const metrics = () => {
    const cw = (board.clientWidth - gap * (cols - 1)) / cols;
    return { cw, left: (x) => x * (cw + gap), width: (w) => w * cw + (w - 1) * gap };
  };
  // items lists the boxes to place, in order, each with its column, width and height.
  const items = () => {
    const l = layout();
    const stack = mode() === "stack";
    return l.order.filter((id) => editing || !l.hidden.includes(id)).map((id) => {
      const [x, w, h] = l.place[id];
      return { el: byId[id], x: stack ? 0 : x, w: stack ? cols : w, h: stack ? 0 : h };
    });
  };
  // pack drops each item, in order, onto the lowest point of the columns it spans.
  const pack = (list, heights) => {
    const sky = new Array(cols).fill(0);
    const out = list.map((it) => {
      const y = Math.max(...sky.slice(it.x, it.x + it.w));
      for (let c = it.x; c < it.x + it.w; c++) sky[c] = y + heights.get(it.el) + gap;
      return { ...it, y };
    });
    return { out, height: Math.max(0, ...sky) - gap };
  };
  // level stretches the content-fitted boxes of each row to the row's
  // tallest, updating heights; it says whether any grew.
  const level = (out, heights) => {
    const rows = new Map();
    for (const it of out) {
      if (it.el === slot || it.h || it.el.classList.contains("off")) continue;
      rows.set(it.y, [...(rows.get(it.y) || []), it.el]);
    }
    let grew = false;
    for (const row of rows.values()) {
      if (row.length < 2) continue;
      const top = Math.max(...row.map((el) => heights.get(el)));
      for (const el of row) {
        if (heights.get(el) === top || heights.get(el) < top * 0.6) continue;
        el.style.minHeight = top + "px";
        heights.set(el, top);
        grew = true;
      }
    }
    return grew;
  };
  const render = (list = items()) => {
    const m = metrics();
    const l = layout();
    for (const b of boxes) b.classList.toggle("off", l.hidden.includes(b.dataset.box));
    for (const it of list) {
      if (it.el === slot) continue;
      // A hidden box shows just its bar while editing, whatever its height.
      const h = it.h && !it.el.classList.contains("off") ? it.h : 0;
      it.el.style.width = m.width(it.w) + "px";
      it.el.style.height = h ? h + "px" : "";
      it.el.style.minHeight = "";
      it.el.classList.toggle("sized", h > 0);
      const size = it.el.querySelector(".bx-w");
      if (size) size.textContent = it.w + "/" + cols + (it.h ? " · " + it.h + "px" : "");
    }
    const heights = new Map(list.map((it) => [it.el, it.el === slot ? drag.h : it.el.offsetHeight]));
    let { out, height } = pack(list, heights);
    if (even && mode() !== "stack" && level(out, heights)) ({ out, height } = pack(list, heights));
    for (const it of out) it.el.style.transform = `translate(${m.left(it.x)}px, ${it.y}px)`;
    if (list.some((it) => it.el === slot)) {
      slot.style.width = m.width(drag.w) + "px";
      slot.style.height = drag.h + "px";
    }
    board.style.height = height + "px";
  };
  board.classList.add("packed");
  render();
  let queued = false;
  const later = () => {
    if (queued || drag) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; render(); });
  };
  const boardObs = new ResizeObserver(later);
  boardObs.observe(board);
  const watch = new ResizeObserver(later);
  for (const b of boxes) watch.observe(b);
  signal.addEventListener("abort", () => { boardObs.disconnect(); watch.disconnect(); }, { once: true });

  const editBtn = document.querySelector("[data-board-edit]");
  const editBar = document.querySelector("[data-board-editbar]");
  if (!editBtn || !editBar) return;

  const icon = (name) => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "i");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#i-" + name);
    svg.append(use);
    return svg;
  };
  const el = (tag, cls, label) => {
    const e = document.createElement(tag);
    e.className = cls;
    if (tag === "button") e.type = "button";
    if (label) { e.title = label; e.setAttribute("aria-label", label); }
    return e;
  };
  for (const b of boxes) {
    const tools = el("div", "bx-bar");
    const grip = el("button", "bx-grip", t("Drag to move %s. Arrow keys move it; Shift with an arrow key resizes it", b.dataset.title));
    grip.append(icon("grip"));
    const name = el("span", "bx-name");
    name.textContent = b.dataset.title;
    const fit = el("button", "bx-fit", t("Fit the height to the content"));
    fit.textContent = t("Fit");
    const eye = el("button", "bx-eye", t("Hide this box"));
    eye.append(icon("eye"));
    tools.append(grip, name, el("span", "bx-w"), fit, eye);
    b.prepend(tools);
    b.append(el("div", "bx-edge l", t("Drag to resize")), el("div", "bx-edge r", t("Drag to resize")),
      el("div", "bx-edge b", t("Drag to resize; double-click to fit the content")), el("div", "bx-edge br", t("Drag to resize")));
  }
  const refresh = () => {
    const l = layout();
    for (const b of boxes) {
      const off = b.classList.contains("off");
      const eye = b.querySelector(".bx-eye");
      eye.title = t(off ? "Show this box" : "Hide this box");
      eye.setAttribute("aria-label", eye.title);
      eye.querySelector("use").setAttribute("href", off ? "#i-eye-off" : "#i-eye");
      b.querySelector(".bx-fit").hidden = !l.place[b.dataset.box][2];
    }
  };
  const live = editBar.querySelector("[data-board-live]");
  const announce = (msg) => { if (live) live.textContent = msg; };
  const setEditing = (on, say = true) => {
    const was = editing;
    editing = on;
    board.classList.toggle("editing", on);
    editBar.hidden = !on;
    editBtn.hidden = on || mode() === "stack";
    for (const b of boxes) b.querySelector(".bx-body").inert = on;
    render();
    refresh();
    // Focus goes somewhere useful: the first handle while editing, the
    // button that opened it afterwards. Leaving says the layout is kept.
    if (on) {
      announce(t("Editing the layout: drag a box, or use the arrow keys on its handle."));
      (board.querySelector(".bx-grip") || editBar.querySelector("[data-board-done]")).focus();
    } else if (was) {
      announce(t("Layout editing finished."));
      if (mode() !== "stack") editBtn.focus();
      if (say && window.hdToast) hdToast(t("Layout saved in this browser"));
    }
  };

  // Escape leaves the layout editor, keeping what was changed.
  document.addEventListener("keydown", (e) => {
    if (editing && e.key === "Escape") {
      e.preventDefault();
      setEditing(false);
    }
  }, { signal });
  editBtn.hidden = mode() === "stack";
  window.addEventListener("resize", () => {
    if (mode() === "stack" && editing) setEditing(false, false);
    editBtn.hidden = editing || mode() === "stack";
  }, { signal });
  editBtn.addEventListener("click", () => setEditing(true));
  editBar.querySelector("[data-board-done]").addEventListener("click", () => setEditing(false));
  editBar.querySelector("[data-board-reset]").addEventListener("click", () => {
    // In the page: window.confirm is suppressed for a reader who ticked
    // "prevent additional dialogs", which left this button doing nothing.
    askInPage(t("Reset this layout to the default?")).then((yes) => {
      if (!yes) return;
      layouts[mode() === "wide" ? "wide" : "std"] = defaults(mode());
      save();
      render();
      refresh();
      announce(t("Layout reset to the default."));
      if (window.hdToast) hdToast(t("Layout reset to the default."));
    });
  });
  const fitHeight = (id) => {
    const p = layout().place[id];
    layout().place[id] = [p[0], p[1], 0];
    save();
    render();
    refresh();
  };

  board.addEventListener("click", (e) => {
    if (!editing) return;
    const id = e.target.closest("[data-box]")?.dataset.box;
    if (e.target.closest(".bx-fit")) {
      fitHeight(id);
    } else if (e.target.closest(".bx-eye")) {
      const l = layout();
      l.hidden = l.hidden.includes(id) ? l.hidden.filter((h) => h !== id) : [...l.hidden, id];
      save();
      render();
      refresh();
    }
  });
  board.addEventListener("dblclick", (e) => {
    if (editing && e.target.closest(".bx-edge.b")) fitHeight(e.target.closest("[data-box]").dataset.box);
  });
  // Keys on the handle: up and down change the order, left and right move a
  // box a column; with Shift, left and right change the width and up and down
  // the height.
  board.addEventListener("keydown", (e) => {
    const grip = e.target.closest(".bx-grip");
    if (!grip || !["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(e.key)) return;
    e.preventDefault();
    const box = grip.closest("[data-box]");
    const id = box.dataset.box;
    const l = layout();
    const [x, w, h] = l.place[id];
    const dir = e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 1;
    const vertical = e.key === "ArrowUp" || e.key === "ArrowDown";
    if (vertical && e.shiftKey) {
      l.place[id] = [x, w, snapH((h || box.offsetHeight) + dir * step)];
    } else if (vertical) {
      const i = l.order.indexOf(id);
      const j = clamp(i + dir, 0, l.order.length - 1);
      [l.order[i], l.order[j]] = [l.order[j], l.order[i]];
    } else if (e.shiftKey) {
      const nw = clamp(w + dir, minW, cols);
      l.place[id] = [Math.min(x, cols - nw), nw, h];
    } else {
      l.place[id] = [clamp(x + dir, 0, cols - w), w, h];
    }
    save();
    render();
    refresh();
    grip.focus();
  });

  // Dragging. Moving: the box follows the pointer, a dashed slot shows where
  // it will land, and the other boxes slide aside. Resizing: the sides snap
  // to the nearest column line, the bottom to 20px steps, and the box
  // reflows as it goes.
  const boardPoint = (e) => {
    const r = board.getBoundingClientRect();
    return [e.clientX - r.left, e.clientY - r.top];
  };
  const moveTo = (e) => {
    const [px, py] = boardPoint(e);
    const left = px - drag.dx;
    const top = py - drag.dy;
    drag.box.style.transform = `translate(${left}px, ${top}px)`;
    const m = metrics();
    const x = clamp(Math.round(left / (m.cw + gap)), 0, cols - drag.w);
    const others = items().filter((it) => it.el !== drag.box);
    const heights = new Map(others.map((it) => [it.el, it.el.offsetHeight]));
    heights.set(slot, drag.h);
    // Try the slot at each place in the order and keep the one that lands
    // closest to where the box is being held.
    let best = null;
    for (let i = 0; i <= others.length; i++) {
      const list = others.slice();
      list.splice(i, 0, { el: slot, x, w: drag.w, h: 0 });
      const d = Math.abs(pack(list, heights).out[i].y - top);
      if (!best || d < best.d - 1) best = { i, d, list };
    }
    if (best.i !== drag.i || x !== drag.x) {
      drag.i = best.i;
      drag.x = x;
      render(best.list);
    }
  };
  const resizeTo = (e) => {
    const [px, py] = boardPoint(e);
    const m = metrics();
    const [x0, w0, h0] = drag.from;
    let [x, w, h] = [x0, w0, h0];
    const line = clamp(Math.round((px + gap / 2) / (m.cw + gap)), 0, cols);
    if (drag.side === "r" || drag.side === "br") w = clamp(line - x0, minW, cols - x0);
    if (drag.side === "l") {
      x = clamp(line, 0, x0 + w0 - minW);
      w = x0 + w0 - x;
    }
    if (drag.side === "b" || drag.side === "br") h = snapH(py - drag.top);
    const p = layout().place[drag.id];
    if (x !== p[0] || w !== p[1] || h !== p[2]) {
      layout().place[drag.id] = [x, w, h];
      render();
      refresh();
    }
  };
  // Near the top or bottom of the window, scroll so a box can travel (or
  // grow) the whole board.
  const autoscroll = () => {
    if (!drag) return;
    const top = 90;
    const bottom = window.innerHeight - 60;
    let dy = 0;
    if (drag.y < top) dy = -Math.ceil((top - drag.y) / 3);
    else if (drag.y > bottom) dy = Math.ceil((drag.y - bottom) / 3);
    if (dy && (drag.kind === "move" || drag.side === "b" || drag.side === "br")) {
      window.scrollBy(0, dy);
      if (drag.kind === "move") moveTo(drag.last); else resizeTo(drag.last);
    }
    drag.frame = requestAnimationFrame(autoscroll);
  };
  const end = (keep) => {
    if (!drag) return;
    const d = drag;
    drag = null;
    cancelAnimationFrame(d.frame);
    board.classList.remove("dragging", "resizing", "resizing-" + d.side);
    d.box.classList.remove("dragging", "resizing");
    const l = layout();
    if (d.kind === "move") {
      slot.remove();
      if (keep && d.i !== undefined) {
        // d.i counts the boxes shown while editing, which is all of them.
        l.order = l.order.filter((id) => id !== d.id);
        l.order.splice(d.i, 0, d.id);
        l.place[d.id] = [d.x, d.w, d.from[2]];
      }
    } else if (!keep) {
      l.place[d.id] = d.from;
    }
    if (keep) save();
    render();
    refresh();
  };

  board.addEventListener("pointerdown", (e) => {
    if (!editing || e.button !== 0 || drag) return;
    const grip = e.target.closest(".bx-grip");
    const edge = e.target.closest(".bx-edge");
    if (!grip && !edge) return;
    e.preventDefault();
    const box = (grip || edge).closest("[data-box]");
    const id = box.dataset.box;
    const [px, py] = boardPoint(e);
    const r = box.getBoundingClientRect();
    const br = board.getBoundingClientRect();
    drag = { box, id, from: layout().place[id].slice(), last: e, y: e.clientY, top: r.top - br.top };
    if (grip) {
      Object.assign(drag, { kind: "move", dx: px - (r.left - br.left), dy: py - drag.top,
        h: box.offsetHeight, w: layout().place[id][1] });
      box.classList.add("dragging");
      board.classList.add("dragging");
      board.append(slot);
      moveTo(e);
    } else {
      const side = ["l", "r", "b", "br"].find((s) => edge.classList.contains(s));
      Object.assign(drag, { kind: "resize", side });
      box.classList.add("resizing");
      board.classList.add("resizing", "resizing-" + side);
    }
    board.setPointerCapture(e.pointerId);
    drag.frame = requestAnimationFrame(autoscroll);
  });
  board.addEventListener("pointermove", (e) => {
    if (!drag) return;
    drag.last = e;
    drag.y = e.clientY;
    if (drag.kind === "move") moveTo(e); else resizeTo(e);
  });
  board.addEventListener("pointerup", () => end(true));
  board.addEventListener("pointercancel", () => end(false));
  board.addEventListener("lostpointercapture", () => end(true));
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (drag) end(false);
    else if (editing) setEditing(false);
  }, { signal });
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init(document, new AbortController().signal);
})();
