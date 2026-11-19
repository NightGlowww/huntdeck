// The API page (api.html): Send runs an endpoint's example request (as
// edited) against this HuntDeck and shows the answer in place of the sample.
// GETs only: nothing here changes anything. The curl example follows edits.

const shownLines = 400; // a longer answer is cut, with a link to all of it

for (const form of document.querySelectorAll("[data-try]")) {
  const ex = form.closest(".ep-ex");
  const input = form.querySelector("input");
  const curl = ex.querySelector("[data-curl]");
  const out = ex.querySelector("[data-try-out]");
  const label = ex.querySelector("[data-try-label]");
  const note = ex.querySelector("[data-try-note]");
  const base = curl.dataset.curl;
  const curlOf = (path) => 'curl -s "' + base + path + '"';
  let last = input.value;
  form.hidden = false;

  input.addEventListener("input", () => {
    if (curl.textContent === curlOf(last)) curl.textContent = curlOf(input.value.trim());
    last = input.value.trim();
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const path = input.value.trim();
    if (!/^\/(api\/|healthz)/.test(path)) {
      show(t("Not sent"), t("only this HuntDeck's API: start with /api/"), "", true);
      return;
    }
    const button = form.querySelector("button");
    button.disabled = true;
    const started = performance.now();
    try {
      const res = await fetch(path, { headers: { Accept: "application/json, text/plain" }, credentials: "same-origin" });
      const body = await res.text();
      const ms = Math.round(performance.now() - started);
      const lines = body.replace(/\n$/, "").split("\n");
      const size = body.length < 1024 ? body.length + " B" : (body.length / 1024).toFixed(1) + " kB";
      const cut = lines.length > shownLines;
      show(t("Response · %s", res.status + (res.ok ? "" : " " + res.statusText)), ms + " ms · " + size + (cut ? " · " + t("first %s lines", shownLines) : ""),
        (cut ? lines.slice(0, shownLines).join("\n") + "\n…" : lines.join("\n")), !res.ok, cut ? path : "");
    } catch (err) {
      show(t("Not sent"), String(err), "", true);
    } finally {
      button.disabled = false;
    }
  });

  function show(head, detail, text, bad, all) {
    label.textContent = head;
    label.classList.toggle("bad", bad);
    note.textContent = detail;
    if (all) {
      const a = document.createElement("a");
      a.className = "link";
      a.href = all;
      a.target = "_blank";
      a.textContent = " · " + t("open all");
      note.append(a);
    }
    out.hidden = text === "";
    out.textContent = text;
  }
}
