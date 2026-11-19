// The page's scripts' words: window.HD_I18N holds the message catalog for
// the interface's language (inlined by the server), and t() looks a message
// up, filling %s / %d from its arguments. A message the catalog doesn't have
// reads as the English source.
window.t = function (msg, ...args) {
  let out = (window.HD_I18N && window.HD_I18N[msg]) || msg;
  for (const a of args) out = out.replace(/%[sd]/, a);
  return out;
};
