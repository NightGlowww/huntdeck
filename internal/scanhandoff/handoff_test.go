package scanhandoff

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// fakeScanner answers the calls the handoff makes and remembers every task it
// was asked to create, so "one per target, once" is a count rather than an
// impression.
type fakeScanner struct {
	mu    sync.Mutex
	names []string
	sizes []int
}

func (f *fakeScanner) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/healthz") {
			_, _ = w.Write([]byte("{\"host\":{\"memAvailablePercent\":70}}"))
			return
		}
		var req struct {
			Params struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		text := "{}"
		switch req.Params.Name {
		case "create_scan_task":
			f.mu.Lock()
			if n, ok := req.Params.Args["name"].(string); ok {
				f.names = append(f.names, n)
			}
			targets, _ := req.Params.Args["target"].(string)
			f.sizes = append(f.sizes, len(strings.Split(targets, "\n")))
			f.mu.Unlock()
			text = `{"id":"task-1"}`
		case "start_task":
			text = `{"ok":true}`
		}
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}},
		})
		_, _ = w.Write(payload)
	}))
}

func newHandoffStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, t.TempDir()+"/h.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return store.New(d, time.Now)
}

// seed writes one open program with the given in-scope domains, through the
// same calls the poll does.
func seed(t *testing.T, st *store.Store, targets []string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Write(ctx, func(tx *store.Tx) error {
		pid, err := tx.UpsertProgram(ctx, "fake", platform.Program{
			Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open", OffersBounty: true,
		})
		if err != nil {
			return err
		}
		for i, target := range targets {
			if _, err := tx.InsertEntry(ctx, pid, strconv.Itoa(i), "hash-"+strconv.Itoa(i), platform.ScopeEntry{
				InScope: true, AssetType: "domain", PlatformAssetType: "Domain",
				Identifier: target, BountyEligible: true,
			}, true); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A target goes over once. The second run must create nothing: that is the
// whole reason scan_handoffs exists, and a handoff that keeps re-queueing the
// same target is a scanner pointed at the same host forever.
func TestHandoffSendsEachTargetOnce(t *testing.T) {
	st := newHandoffStore(t)
	ctx := context.Background()
	seed(t, st, []string{"a.example.com", "b.example.com"})
	if err := st.SaveScanConfig(ctx, store.ScanConfig{
		Enabled: true, TemplateID: "tpl", NodeName: "node", PerTask: 1,
	}); err != nil {
		t.Fatal(err)
	}
	f := &fakeScanner{}
	srv := f.server(t)
	defer srv.Close()
	client := &scopesentry.Client{BaseURL: srv.URL, Key: "ssk_test"}

	created, err := Run(ctx, st, client, true)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if created != 2 {
		t.Fatalf("first run created %d tasks, want 2 (one per target at PerTask=1)", created)
	}
	created, err = Run(ctx, st, client, true)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if created != 0 {
		t.Fatalf("second run created %d tasks, want 0: the same targets were queued again", created)
	}
	if len(f.names) != 2 {
		t.Errorf("the scanner was asked for %d tasks in total, want 2", len(f.names))
	}
	for i, n := range f.sizes {
		if n != 1 {
			t.Errorf("task %d carried %d targets, want 1", i, n)
		}
	}
}

// Nothing is created when the operator has not turned it on: the schedule runs
// whether or not anyone asked for it.
func TestHandoffDoesNothingWhileOff(t *testing.T) {
	st := newHandoffStore(t)
	ctx := context.Background()
	seed(t, st, []string{"a.example.com"})
	if err := st.SaveScanConfig(ctx, store.ScanConfig{TemplateID: "tpl", NodeName: "node", PerTask: 5}); err != nil {
		t.Fatal(err)
	}
	f := &fakeScanner{}
	srv := f.server(t)
	defer srv.Close()
	created, err := Run(ctx, st, &scopesentry.Client{BaseURL: srv.URL, Key: "ssk"}, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if created != 0 || len(f.names) != 0 {
		t.Fatalf("created %d tasks while off (scanner saw %d)", created, len(f.names))
	}
}
