package web

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestListUXGuards(t *testing.T) {
	t.Run("queue_copy_and_row_nav", func(t *testing.T) {
		e := newTestEnv(t, func(*Deps) {})
		if err := e.store.RefreshHuntQueue(context.Background(), time.Now()); err != nil {
			t.Fatal(err)
		}
		res, body := e.do(e.client(), http.MethodGet, "/queue", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("queue page: %d %.200s", res.StatusCode, body)
		}
		start := strings.Index(body, "<h2>Start today</h2>")
		if start < 0 {
			t.Fatal("missing Start today section")
		}
		section := body[start:]
		if end := strings.Index(section, "</section>"); end > 0 {
			section = section[:end]
		}
		if !strings.Contains(section, `data-href="/programs/`) {
			t.Error("start-today rows lack data-href row navigation")
		}
		if !strings.Contains(section, `data-copy-text="`) {
			t.Error("start-today rows lack data-copy-text copy button")
		}
	})

	t.Run("targets_i18n_and_live_total", func(t *testing.T) {
		b, err := os.ReadFile("templates/targets.html")
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, `<span class="total" role="status" aria-live="polite">`) {
			t.Error("targets.html .total lacks role=\"status\" aria-live=\"polite\"")
		}
		if strings.Contains(src, `<span class="upto">from</span>`) || !strings.Contains(src, `<span class="upto">{{T "from"}}</span>`) {
			t.Error("targets.html .upto must use {{T \"from\"}}")
		}
		if strings.Contains(src, `data-label="Score"`) || !strings.Contains(src, `data-label="{{T "Score"}}"`) {
			t.Error("targets.html c-score must use data-label=\"{{T \"Score\"}}\"")
		}
	})

	t.Run("app_js_sidebar_i18n_and_single_status_poll", func(t *testing.T) {
		b, err := os.ReadFile("static/app.js")
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, want := range []string{
			`t("Show the sidebar's names")`,
			`t("Fold the sidebar to its icons")`,
			`t("Unfold the sidebar")`,
			`t("Fold the sidebar")`,
		} {
			if !strings.Contains(src, want) {
				t.Errorf("app.js lacks %s", want)
			}
		}
		if got := strings.Count(src, `fetch("/api/v1/status"`); got != 1 {
			t.Errorf("app.js has %d fetch(\"/api/v1/status\" calls, want 1", got)
		}
	})
}
