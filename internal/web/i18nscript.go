package web

import (
	"encoding/json"
	"net/http"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// i18nScript serves the scripts' messages for the visitor's language:
// window.HD_I18N, which static/i18n.js's t() reads. A file rather than an
// inline script, so the page's CSP needs no nonce for it.
func (s *server) i18nScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // the language follows a cookie
	w.Header().Set("X-Content-Type-Options", "nosniff")
	msgs := i18n.JS(s.langOf(r))
	if len(msgs) == 0 {
		_, _ = w.Write([]byte("window.HD_I18N = null;\n"))
		return
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		_, _ = w.Write([]byte("window.HD_I18N = null;\n"))
		return
	}
	_, _ = w.Write(append(append([]byte("window.HD_I18N = "), b...), []byte(";\n")...))
}
