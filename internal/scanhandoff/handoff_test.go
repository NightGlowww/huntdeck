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
	// startErr, when set, is the JSON-RPC error text start_task answers with:
	// a scanner that creates a task and refuses to start it.
	startErr string
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
			if f.startErr != "" {
				// A tool-level failure: the RPC succeeded and the tool said no, which
				// is how the scanner reports a full queue.
				payload, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": map[string]any{
						"content": []map[string]string{{"type": "text", "text": f.startErr}},
						"isError": true,
					},
				})
				_, _ = w.Write(payload)
				return
			}
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
	// The schedule is off on purpose: the operator pressing "send now" is the
	// whole point of manual, and with Enabled true the flag never decides
	// anything (changing it to if !cfg.Enabled used to pass every test).
	if err := st.SaveScanConfig(ctx, store.ScanConfig{
		Enabled: false, TemplateID: "tpl", NodeName: "node", PerTask: 1,
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
	// A candidate to send: without one, the handoff has nothing to do and
	// returns success before it ever reaches the scanner.
	if err := st.Write(ctx, func(tx *store.Tx) error {
		id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", State: "open", OffersBounty: true})
		if err != nil {
			return err
		}
		if _, err := tx.InsertEntry(ctx, id, "a.example.com", "a.example.com",
			platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: "a.example.com"}, true); err != nil {
			return err
		}
		return tx.RefreshScopeCounts(ctx, id)
	}); err != nil {
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

// A task must belong to one program. The first version sliced the candidate list
// by count alone, so a batch could carry targets from two programs under a name
// that named neither - huntdeck-20261120-161552-1, which says nothing about what
// is being scanned and cannot be attributed to a scope.
func TestTasksCarryOneProgramAndSayWhich(t *testing.T) {
	cands := []store.ScanCandidate{
		{Target: "a.acme.example", Platform: "hackerone", Handle: "acme"},
		{Target: "b.other.example", Platform: "hackerone", Handle: "other"},
		{Target: "c.acme.example", Platform: "hackerone", Handle: "acme"},
	}
	groups := groupByProgram(cands)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].handle != "acme" || len(groups[0].items) != 2 {
		t.Fatalf("first group = %+v", groups[0])
	}
	if groups[1].handle != "other" || len(groups[1].items) != 1 {
		t.Fatalf("second group = %+v", groups[1])
	}
	for _, g := range groups {
		for _, c := range g.items {
			if c.Handle != g.handle {
				t.Fatalf("group %s holds a target of %s", g.handle, c.Handle)
			}
		}
	}
}

// The name has to say which program: a list of huntdeck-<stamp>-N tells the
// operator nothing about what is being scanned.
func TestTaskNameNamesTheProgram(t *testing.T) {
	first := taskName("bugcrowd", "opensea", 1)
	if !strings.HasPrefix(first, "huntdeck-bugcrowd-opensea-") {
		t.Fatalf("first task = %q", first)
	}
	if strings.HasSuffix(first, "-1") {
		t.Errorf("the first task carries a counter: %q", first)
	}
	second := taskName("bugcrowd", "opensea", 2)
	if !strings.HasSuffix(second, "-2") {
		t.Errorf("the second task = %q, want a counter", second)
	}
	if got := taskName("hackerone", "Some_Org/../x", 1); strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Errorf("the name carries characters a task list cannot show: %q", got)
	}
}

// Intigriti's handles are UUIDs (d74a9b6a-0356-46e7-8346-2b9de8ac...), so a name
// built from the handle is unreadable: huntdeck-intigriti-d74a9b6a-0356-46e7-...
// The name is what a person searches the task list for; the handle is only the
// identity that keeps two programs apart.
func TestTaskNameUsesTheProgramNameNotItsUUID(t *testing.T) {
	cands := []store.ScanCandidate{
		{Target: "a.example.com", Platform: "intigriti", Handle: "d74a9b6a-0356-46e7-8346-2b9de8ac", Name: "Acme Corp"},
		{Target: "b.example.com", Platform: "intigriti", Handle: "d74a9b6a-0356-46e7-8346-2b9de8ac", Name: "Acme Corp"},
	}
	groups := groupByProgram(cands)
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	if groups[0].label != "Acme Corp" {
		t.Fatalf("label = %q, want the program name", groups[0].label)
	}
	name := taskName(groups[0].platform, groups[0].label, 1)
	if strings.Contains(name, "d74a9b6a") {
		t.Fatalf("the task name carries the UUID: %q", name)
	}
	if !strings.HasPrefix(name, "huntdeck-intigriti-acme-corp-") {
		t.Fatalf("name = %q", name)
	}
}

// A source that has no name keeps its handle rather than an empty label.
func TestTaskNameFallsBackToTheHandle(t *testing.T) {
	groups := groupByProgram([]store.ScanCandidate{{Target: "a.example.com", Platform: "bugcrowd", Handle: "opensea"}})
	if groups[0].label != "opensea" {
		t.Fatalf("label = %q", groups[0].label)
	}
}

// The same host can sit in two programs' scope - the scan page lists
// info.shop-apotheke.com twice - and a handoff that sends it twice scans it
// twice. The first program keeps it.
func TestTheSameTargetIsHandedOverOnce(t *testing.T) {
	cands := []store.ScanCandidate{
		{Target: "info.example.com", Platform: "intigriti", Handle: "a", Name: "A"},
		{Target: "info.example.com", Platform: "intigriti", Handle: "b", Name: "B"},
		{Target: "other.example.com", Platform: "intigriti", Handle: "b", Name: "B"},
	}
	groups := groupByProgram(cands)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if len(groups[0].items) != 1 || groups[0].handle != "a" {
		t.Fatalf("the first program should keep the shared host: %+v", groups[0])
	}
	if len(groups[1].items) != 1 || groups[1].items[0].Target != "other.example.com" {
		t.Fatalf("second group = %+v", groups[1])
	}
	total := 0
	for _, g := range groups {
		total += len(g.items)
	}
	if total != 2 {
		t.Fatalf("%d targets would be sent, want 2", total)
	}
}

// The queue ranks a URL entry by its host, so "community.personio.com" in the
// queue is the entry https://community.personio.com/... - naming the host has to
// find that entry, or the target the operator picked cannot be sent.
func TestOnlyTargetsFindsAUrlEntryByItsHost(t *testing.T) {
	cands := []store.ScanCandidate{
		{Target: "https://community.example.com/join", Host: "community.example.com", Platform: "intigriti", Handle: "a", Name: "A"},
		{Target: "api.example.com", Host: "api.example.com", Platform: "hackerone", Handle: "b", Name: "B"},
	}
	got := onlyTargets(cands, []string{"community.example.com", "api.example.com"})
	if len(got) != 2 {
		t.Fatalf("matched %d of 2: %+v", len(got), got)
	}
	if got[0].Target != "https://community.example.com/join" {
		t.Fatalf("the host matched the wrong entry: %+v", got[0])
	}
}

// An exact identifier still wins over a host match.
func TestOnlyTargetsPrefersTheIdentifier(t *testing.T) {
	cands := []store.ScanCandidate{
		{Target: "api.example.com", Host: "api.example.com", Platform: "hackerone", Handle: "b"},
	}
	got := onlyTargets(cands, []string{"api.example.com"})
	if len(got) != 1 || got[0].Target != "api.example.com" {
		t.Fatalf("exact match failed: %+v", got)
	}
}
