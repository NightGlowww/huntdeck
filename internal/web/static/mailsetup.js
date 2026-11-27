// Email › HuntMail › Setup, while a setup runs: asks every second or so how far
// it's got, and reloads the page when a step finishes or the run ends
// (requests fail for a moment while HuntDeck restarts to use the new key).
(() => {
  const init = (_root, signal) => {
    const run = document.querySelector("[data-setup-running]");
    if (!run) return;
    const seen = run.querySelectorAll(".msetup-steps li").length;
    let timer = 0;

    async function check() {
      if (signal.aborted) return;
      try {
        const res = await fetch("/email/setup/status", { cache: "no-store", headers: { Accept: "application/json" }, signal });
        if (res.ok) {
          const st = await res.json();
          if (st.saved) {
            location.replace("/email/setup?msg=huntmail-set-up#acct-huntmail");
            return;
          }
          if (!st.running || st.steps !== seen) {
            location.replace("/email/setup");
            return;
          }
        }
      } catch {
        // restarting: try again
      }
      if (!signal.aborted) timer = setTimeout(check, 1200);
    }

    timer = setTimeout(check, 800);
    signal.addEventListener("abort", () => clearTimeout(timer), { once: true });
  };
  if (window.HuntDeck) window.HuntDeck.onMount(init);
  else init(document, new AbortController().signal);
})();
