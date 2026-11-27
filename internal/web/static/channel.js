// The channel form (notification.html): the preview follows what's ticked,
// and "Send a test" says how it went without leaving the form. Without
// this script the form still works: the test button posts the form and
// the page comes back with the outcome.
(() => {
  const init = () => {
  const form = document.getElementById("channel");
  if (!form || form.dataset.channelReady) return;
  form.dataset.channelReady = "1";

  // The preview gets the form's choices, never its address or secrets.
  const choice = /^(event_|platform_)|^(bounty_only|private_only|major_scope_only|min_bounty|tags|exclude_tags|instant|digest_at|enabled)$/;
  const body = document.querySelector("[data-preview-body]");
  let timer = 0, latest = 0;
  const refresh = () => {
    const q = new URLSearchParams();
    for (const [k, v] of new FormData(form)) if (choice.test(k)) q.append(k, v);
    const n = ++latest;
    fetch(form.dataset.preview + "?" + q, { headers: { Accept: "text/html" } })
      .then((r) => (r.ok ? r.text() : Promise.reject(r.status)))
      .then((html) => { if (n === latest) body.innerHTML = html; }) // our own escaped template
      .catch(() => {});
  };
  const later = (e) => {
    if (!body || !e.target.name || !choice.test(e.target.name)) return;
    clearTimeout(timer);
    timer = setTimeout(refresh, 250);
  };
  form.addEventListener("change", later);
  form.addEventListener("input", later);

  const test = form.querySelector("[data-test]");
  const out = form.querySelector("[data-test-result]");
  if (!test || !out) return;
  test.addEventListener("click", (e) => {
    e.preventDefault();
    out.textContent = t("Sending…");
    delete out.dataset.state;
    test.disabled = true;
    fetch(test.formAction + "?inline=1", { method: "POST", body: new URLSearchParams(new FormData(form)) })
      .then(async (r) => {
        out.textContent = await r.text();
        out.dataset.state = r.ok ? "ok" : "err";
      })
      .catch(() => {
        out.textContent = t("Couldn't reach HuntDeck.");
        out.dataset.state = "err";
      })
      .finally(() => { test.disabled = false; });
  });
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init();
})();
