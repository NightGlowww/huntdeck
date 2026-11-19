// A poll interval is typed as "30m" or "6h" or "off". The number is easy; the
// unit is where a typo hides, and the limits (quick 5m or slower, full 1h or
// slower) are in a paragraph far from the field. So the field says back what it
// heard, as it is typed: "every 30 minutes", "off: no scheduled polls", or the
// limit it breaks.
(() => {
  const units = { m: "minute", h: "hour", d: "day" };
  const limits = { quick: 5, full: 60 }; // minutes

  const parse = (raw) => {
    const s = raw.trim().toLowerCase();
    if (s === "off") return { off: true };
    const m = /^(\d+)([mhd])$/.exec(s);
    if (!m) return null;
    const n = Number(m[1]);
    if (n <= 0) return null;
    const unit = units[m[2]];
    return { minutes: m[2] === "m" ? n : m[2] === "h" ? n * 60 : n * 1440, n, unit };
  };

  const phrase = (p) => {
    if (p.off) return t("off: no scheduled polls");
    const plural = p.n === 1 ? p.unit : p.unit + "s";
    return t("every %s %s", p.n, t(plural));
  };

  const describe = (input) => {
    const hint = input.parentElement && input.parentElement.querySelector(".ival-hint");
    if (!hint) return;
    const raw = input.value.trim();
    if (!raw) {
      hint.textContent = "";
      hint.className = "ival-hint";
      return;
    }
    const p = parse(raw);
    if (!p) {
      hint.textContent = t("not an interval: try 30m, 6h, 1d or off");
      hint.className = "ival-hint bad";
      return;
    }
    const kind = input.dataset.kind;
    const floor = limits[kind];
    if (floor && !p.off && p.minutes < floor) {
      hint.textContent = t("%s is faster than the %s floor", phrase(p), kind === "quick" ? "5m" : "1h");
      hint.className = "ival-hint bad";
      return;
    }
    hint.textContent = phrase(p);
    hint.className = "ival-hint";
  };

  const init = () => {
    for (const input of document.querySelectorAll("input.interval")) {
      if (input.dataset.parsed) continue;
      input.dataset.parsed = "1";
      describe(input);
      input.addEventListener("input", () => describe(input));
    }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
