// The API page (api.html):
// - Progressively highlights {param} path tokens and enum value chips
// - Provides live cURL / HTTP / Python snippet tabs on every endpoint card
// - Runs GET requests via [data-try] against this HuntDeck and formats response
//   status badge, latency, size, and JSON output in the dark console.

const shownLines = 400; // a longer answer is cut, with a link to all of it
const hasAuth = document.querySelector(".apid")?.dataset.auth === "1";

// Highlight {param} tokens inside endpoint summary paths without altering
// server-rendered HTML structure.
for (const code of document.querySelectorAll(".ep-path code")) {
  const raw = code.textContent || "";
  if (!raw.includes("{")) continue;
  const parts = raw.split(/(\{[^}]+\})/g);
  const frag = document.createDocumentFragment();
  for (const part of parts) {
    if (!part) continue;
    if (part.startsWith("{") && part.endsWith("}")) {
      const span = document.createElement("span");
      span.className = "ep-param";
      span.textContent = part;
      frag.append(span);
    } else {
      frag.append(document.createTextNode(part));
    }
  }
  code.textContent = "";
  code.append(frag);
}

// Render "a · b · c" enum values as individual monospace chips.
for (const box of document.querySelectorAll(".pvals")) {
  const raw = (box.textContent || "").trim();
  if (!raw) continue;
  const items = raw.split(" · ").map((s) => s.trim()).filter(Boolean);
  if (items.length <= 1 && !raw.includes(" · ")) continue;
  box.textContent = "";
  for (const val of items) {
    const chip = document.createElement("code");
    chip.className = "penum";
    chip.textContent = val;
    box.append(chip);
  }
}

// Language switcher + live request runner per endpoint playground.
for (const ex of document.querySelectorAll(".ep-ex")) {
  const curl = ex.querySelector("[data-curl]");
  if (!curl) continue;

  const form = ex.querySelector("[data-try]");
  const input = form ? form.querySelector("input") : null;
  const out = ex.querySelector("[data-try-out]");
  const label = ex.querySelector("[data-try-label]");
  const note = ex.querySelector("[data-try-note]");
  const respCard = ex.querySelector("[data-resp-card]");
  const respCopy = ex.querySelector("[data-resp-copy]");
  const tabsHost = ex.querySelector(".console-tabs");

  const base = (curl.dataset.curl || location.origin).replace(/\/$/, "");
  const method = (curl.dataset.method || "GET").toUpperCase();
  const defaultPath = curl.dataset.tryDefault || "";
  const bodyType = curl.dataset.bodyType || "";
  const initialCurl = curl.textContent || "";

  let activeLang = "curl";

  const currentPath = () => {
    const raw = input ? input.value.trim() : defaultPath;
    return raw || defaultPath;
  };

  const curlOf = (path) => {
    if (path === defaultPath && initialCurl) return initialCurl;
    if (method === "GET") return 'curl -s "' + base + path + '"';
    return "curl -s -X " + method + ' "' + base + path + '"';
  };

  const httpOf = (path) => {
    let host = "127.0.0.1:8080";
    try {
      host = new URL(base).host;
    } catch {
      /* fallback */
    }
    const lines = [method + " " + path + " HTTP/1.1", "Host: " + host];
    if (hasAuth) lines.push("Authorization: Bearer <TOKEN>");
    lines.push("Accept: application/json, text/plain");
    if (bodyType) lines.push("Content-Type: " + bodyType);
    return lines.join("\n");
  };

  const pythonOf = (path) => {
    const url = base + path;
    const isText =
      path.includes("scope.") ||
      path.includes("format=txt") ||
      (path.startsWith("/api/v1/targets") && !path.includes("format=json"));
    const lines = ["import requests", "", 'url = "' + url + '"'];
    const args = ["url"];
    if (hasAuth) {
      lines.push('headers = {"Authorization": "Bearer <TOKEN>"}');
      args.push("headers=headers");
    }
    if (bodyType) {
      args.push('data="..."');
    }
    lines.push("r = requests." + method.toLowerCase() + "(" + args.join(", ") + ")");
    lines.push(isText ? "print(r.text)" : "print(r.json())");
    return lines.join("\n");
  };

  const renderSnippet = () => {
    const path = currentPath();
    if (activeLang === "http") curl.textContent = httpOf(path);
    else if (activeLang === "python") curl.textContent = pythonOf(path);
    else curl.textContent = curlOf(path);
  };

  if (tabsHost) {
    const langs = [
      ["curl", "cURL"],
      ["http", "HTTP"],
      ["python", "Python"],
    ];
    tabsHost.textContent = "";
    const buttons = [];
    for (const [id, title] of langs) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "console-tab" + (id === activeLang ? " is-active" : "");
      btn.setAttribute("role", "tab");
      btn.setAttribute("aria-selected", id === activeLang ? "true" : "false");
      btn.dataset.lang = id;
      btn.textContent = title;
      btn.addEventListener("click", () => {
        activeLang = id;
        for (const b of buttons) {
          const on = b === btn;
          b.classList.toggle("is-active", on);
          b.setAttribute("aria-selected", on ? "true" : "false");
        }
        renderSnippet();
      });
      buttons.push(btn);
      tabsHost.append(btn);
    }
  }

  if (!form) continue;
  form.hidden = false;

  input.addEventListener("input", () => {
    renderSnippet();
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const path = input.value.trim();
    if (!/^\/(api\/|healthz)/.test(path)) {
      show(t("Not sent"), t("only this HuntDeck's API: start with /api/"), "", true);
      return;
    }
    const button = form.querySelector("button");
    const idleLabel = button.textContent;
    button.disabled = true;
    button.textContent = t("Sending…");
    const started = performance.now();
    try {
      const res = await fetch(path, {
        headers: { Accept: "application/json, text/plain" },
        credentials: "same-origin",
      });
      const raw = await res.text();
      const ms = Math.round(performance.now() - started);
      let formatted = raw;
      const ctype = res.headers.get("Content-Type") || "";
      if (ctype.includes("json") && raw.trim()) {
        try {
          formatted = JSON.stringify(JSON.parse(raw), null, 2);
        } catch {
          formatted = raw;
        }
      }
      const lines = formatted.replace(/\n$/, "").split("\n");
      const size = raw.length < 1024 ? raw.length + " B" : (raw.length / 1024).toFixed(1) + " kB";
      const cut = lines.length > shownLines;
      const statusText = res.status + " " + (res.statusText || (res.ok ? "OK" : ""));
      show(
        t("Response · %s", statusText.trim()),
        ms + " ms · " + size + (cut ? " · " + t("first %s lines", shownLines) : ""),
        cut ? lines.slice(0, shownLines).join("\n") + "\n…" : lines.join("\n"),
        !res.ok,
        cut ? path : ""
      );
    } catch (err) {
      show(t("Not sent"), String(err), "", true);
    } finally {
      button.disabled = false;
      button.textContent = idleLabel;
    }
  });

  function show(head, detail, text, bad, all) {
    if (respCard) respCard.hidden = false;
    label.textContent = head;
    label.classList.toggle("bad", bad);
    label.classList.toggle("is-bad", bad);
    label.classList.toggle("is-ok", !bad);
    label.classList.remove("is-sample");
    note.textContent = detail;
    if (all) {
      const a = document.createElement("a");
      a.className = "link";
      a.href = all;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.textContent = " · " + t("open all");
      note.append(a);
    }
    out.hidden = text === "";
    out.textContent = text;
    if (respCopy) respCopy.hidden = text === "";
  }
}
