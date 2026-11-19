// The password field shows when Caps Lock is on: a refused password is
// usually a capitalised one.
(() => {
  const field = document.getElementById("password");
  const hint = document.getElementById("pw-caps");
  if (!field || !hint) return;
  const show = (on) => { hint.hidden = !on; };
  const check = (e) => {
    if (typeof e.getModifierState !== "function") return;
    show(e.getModifierState("CapsLock"));
  };
  field.addEventListener("keydown", check);
  field.addEventListener("keyup", check);
  field.addEventListener("blur", () => show(false));
})();
