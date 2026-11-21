// Applies the saved theme and palette (a hidden demo banner, an opened
// filter rail) before first paint (loaded without
// defer). "system" (or nothing saved) leaves light or dark to
// prefers-color-scheme; no saved palette means the default, Midnight.
// Also marks the page as running scripts, so the hunt board can stay hidden
// until board.js has placed its boxes rather than showing them stacked first.
(function () {
  document.documentElement.classList.add("js");
  try {
    var t = localStorage.getItem("huntdeck-theme");
    if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
    var p = localStorage.getItem("huntdeck-palette");
    if (p === "nord" || p === "graphite" || p === "bluewhite" || p === "techblue") document.documentElement.dataset.palette = p;
    // A demo's banner, once hidden in this browser, stays hidden.
    if (localStorage.getItem("huntdeck-demo-banner") === "off") document.documentElement.dataset.demoBanner = "off";
    // The filter rail, left open on a narrower screen, opens again.
    if (localStorage.getItem("huntdeck-filters") === "open") document.documentElement.dataset.filters = "open";
    // The text size, chosen in Settings › Display.
    var f = localStorage.getItem("huntdeck-fs");
    if (f === "sm" || f === "lg" || f === "xl") document.documentElement.dataset.fs = f;
  } catch (e) { /* storage blocked: follow the system */ }
})();
