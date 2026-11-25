// The Email page on a wide screen: a row opens its message in the pane on
// the right (GET /email/{id}/pane), and the address keeps it (?open=), so
// a reload or Back shows it again. Narrower screens, and a middle, Ctrl or
// Cmd click, open the message's own page, as without this script. The
// pane's HTML is the server's, escaped like any page.
const pane = document.querySelector("[data-mail-pane]");
const wide = matchMedia("(min-width: 1280px)");

if (pane) {
  const list = pane.parentElement.querySelector(".mlist");
  let asked = 0;

  const show = async (row) => {
    const id = row.dataset.mail;
    for (const r of list.querySelectorAll("tr.on")) { r.classList.remove("on"); r.removeAttribute("aria-current"); }
    row.classList.add("on");
    row.setAttribute("aria-current", "true");
    const mine = ++asked;
    pane.setAttribute("aria-busy", "true");
    let html;
    try {
      const res = await fetch("/email/" + encodeURIComponent(id) + "/pane", { headers: { Accept: "text/html" } });
      html = res.ok ? await res.text() : null;
    } catch {
      html = null;
    }
    if (mine !== asked) return; // another row was opened since
    pane.removeAttribute("aria-busy");
    if (html === null) {
      const box = document.createElement("section");
      box.className = "panel";
      box.innerHTML = '<div class="empty-state err">Couldn\'t open that message. Try again, or open it on its own page.</div>';
      pane.replaceChildren(box);
    } else {
      pane.innerHTML = html;
    }
    pane.scrollTop = 0;
    const u = new URL(location.href);
    u.searchParams.set("open", id);
    history.replaceState(null, "", u);
  };

  // Before app.js's row links (capture): only a plain click, not on the
  // row's own links and buttons (its program, code, copy, verify link);
  // the subject's link opens in the pane like the row.
  list?.addEventListener("click", (e) => {
    if (!wide.matches || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    const row = e.target.closest("tr[data-mail]");
    if (!row || String(getSelection())) return;
    const own = e.target.closest("a, button, input, [data-norow]");
    if (own && !own.closest(".c-subj")) return;
    e.preventDefault();
    e.stopPropagation();
    show(row);
  }, true);

  // o or Enter on the row j and k marked (keys.js) opens it here too.
  document.addEventListener("keydown", (e) => {
    if (!wide.matches || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    if (e.key !== "o" && e.key !== "Enter") return;
    if (e.target !== document.body) return;
    const row = list?.querySelector("tr.kb-row[data-mail]");
    if (!row) return;
    e.preventDefault();
    show(row);
  }, true);
}
