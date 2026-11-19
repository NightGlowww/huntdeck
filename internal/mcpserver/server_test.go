package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// kinds is what the tools asked the fake for, so a test can check the
// defaults they send.
var (
	kindsMu sync.Mutex
	kinds   []string
)

// instance is a stand-in for HuntDeck: it asks for a password, then answers
// the API routes the tools use.
func instance(t *testing.T, password string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("password") != password {
			http.Error(w, "wrong", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "hd_session", Value: "s3cret", Path: "/"})
		w.WriteHeader(http.StatusSeeOther)
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("hd_session"); err != nil || c.Value != "s3cret" {
				http.Error(w, "no session", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/v1/status", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"programs":1328,"platforms":["hackerone"]}`)
	}))
	// The queue echoes whether it was asked for the factor breakdown, which is
	// what a tool has to pass through faithfully.
	mux.HandleFunc("/api/v1/queue", auth(func(w http.ResponseWriter, r *http.Request) {
		// The query comes back as the row's value, so a test can see what the tool
		// sent without changing the shape of the answer.
		body := `{"computed_at":"2026-11-19T00:00:00Z","rows":[{"platform":"hackerone","handle":"acme","kind":"domain","value":"` + r.URL.RawQuery + `","score":61.5,"reasons":["pays"]`
		if r.URL.Query().Get("explain") == "1" {
			body += `,"factors":[{"name":"quality","value":0.5,"weight":0.3,"effect":0.81}]`
		}
		// Close the row object before the array: the body used to end with
		// `["pays"]]}` — invalid JSON that no test had parsed until one did.
		_, _ = io.WriteString(w, body+"}]}")
	}))
	mux.HandleFunc("/api/v1/invites", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"programs":[{"platform":"hackerone","handle":"acme","private":true}],"total":1}`)
	}))
	mux.HandleFunc("/api/v1/board", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"totals":{"programs":1},"picks":[]}`)
	}))
	mux.HandleFunc("/api/v1/stats", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"totals":[],"platforms":[]}`)
	}))
	mux.HandleFunc("/api/v1/changes", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"total":0,"changes":[]}`)
	}))
	mux.HandleFunc("/api/v1/disclosed", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"total":0,"reports":[]}`)
	}))
	mux.HandleFunc("/api/v1/programs", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = io.WriteString(w, "platform,handle\nfake,acme\n")
			return
		}
		if got := r.URL.Query().Get("q"); got != "acme" {
			t.Errorf("q = %q", got)
		}
		_, _ = io.WriteString(w, `{"total":1,"programs":[{"handle":"acme"}]}`)
	}))
	mux.HandleFunc("/api/v1/programs/hackerone/acme/scope.burp.json", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"target":{"scope":{"advanced_mode":true,"include":[]}}}`)
	}))
	mux.HandleFunc("/changes", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "txt" {
			t.Errorf("changes format = %q", r.URL.Query().Get("format"))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "2026-11-17\thackerone/acme\tScope added: a.example.com\ta.example.com\n")
	}))
	mux.HandleFunc("/api/v1/targets", auth(func(w http.ResponseWriter, r *http.Request) {
		kindsMu.Lock()
		kinds = append(kinds, r.URL.Query().Get("kind"))
		kindsMu.Unlock()
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("targets format = %q", got)
		}
		if s := r.URL.Query().Get("sort"); s != "" && s != "best" {
			t.Errorf("targets sort = %q", s)
		}
		_, _ = io.WriteString(w, `{"targets":[{"value":"a.example.com"}]}`)
	}))
	mux.HandleFunc("/api/v1/match", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("bulk match used %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		lines := strings.Count(strings.TrimSpace(string(body)), "\n") + 1
		_, _ = fmt.Fprintf(w, "{\"in\":0,\"out\":%d,\"none\":0}", lines)
	}))
	mux.HandleFunc("/api/v1/poll/", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"started":true}`)
	}))
	mux.HandleFunc("/api/v1/programs/hackerone/acme", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"handle":"acme","name":"Acme"}`)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// session feeds requests to Run and collects the answers by id.
func session(t *testing.T, s *Server, requests ...string) map[string]json.RawMessage {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	answers := map[string]json.RawMessage{}
	dec := json.NewDecoder(&out)
	for {
		var m map[string]json.RawMessage
		if err := dec.Decode(&m); err != nil {
			break
		}
		if id, ok := m["id"]; ok {
			answers[string(id)] = m["result"]
		}
	}
	return answers
}

func TestInitializeAndToolsList(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	got := session(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	var init struct {
		ProtocolVersion string                `json:"protocolVersion"`
		ServerInfo      struct{ Name string } `json:"serverInfo"`
	}
	if err := json.Unmarshal(got["1"], &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion == "" || init.ServerInfo.Name != "huntdeck" {
		t.Errorf("initialize = %s", got["1"])
	}
	var list struct {
		Tools []struct{ Name, Description string } `json:"tools"`
	}
	if err := json.Unmarshal(got["2"], &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) < 8 {
		t.Fatalf("%d tools", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if !strings.HasPrefix(tool.Name, "huntdeck_") || tool.Description == "" {
			t.Errorf("tool %q is not declared properly", tool.Name)
		}
	}
}

func TestToolCalls(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	got := session(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"huntdeck_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"huntdeck_programs","arguments":{"q":"acme"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"huntdeck_program","arguments":{"platform":"hackerone","handle":"acme"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"huntdeck_nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"huntdeck_targets","arguments":{"limit":1}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"huntdeck_export","arguments":{"platform":"hackerone","handle":"acme"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"huntdeck_programs_csv","arguments":{"platform":"fake","q":"acme"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"huntdeck_targets","arguments":{"kind":"wildcard","sort":"best","limit":1}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"huntdeck_changes_text","arguments":{"since":"7d","group":"scope"}}}`,
	)
	var res struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	text := func(id string) string {
		if err := json.Unmarshal(got[id], &res); err != nil {
			t.Fatalf("answer %s: %v", id, err)
		}
		if len(res.Content) == 0 {
			return ""
		}
		return res.Content[0].Text
	}
	if !strings.Contains(text("1"), "1328") {
		t.Errorf("status = %s", got["1"])
	}
	if !strings.Contains(text("2"), "acme") {
		t.Errorf("programs = %s", got["2"])
	}
	if !strings.Contains(text("3"), "Acme") {
		t.Errorf("program = %s", got["3"])
	}
	if err := json.Unmarshal(got["4"], &res); err != nil || !res.IsError {
		t.Errorf("an unknown tool isn't reported as an error: %s", got["4"])
	}
	if !strings.Contains(text("5"), "a.example.com") {
		t.Errorf("targets = %s", got["5"])
	}
	if !strings.Contains(text("6"), "advanced_mode") {
		t.Errorf("export = %s", got["6"])
	}
	if !strings.Contains(text("7"), "platform,handle") {
		t.Errorf("programs csv = %s", got["7"])
	}
	if !strings.Contains(text("8"), "a.example.com") {
		t.Errorf("targets sorted by score = %s", got["8"])
	}
	if cols := strings.Split(strings.TrimSpace(text("9")), "\t"); len(cols) != 4 {
		t.Errorf("changes text has %d columns: %q", len(cols), text("9"))
	}
	kindsMu.Lock()
	defer kindsMu.Unlock()
	// One tool asks for a kind by name, the other leaves it out and must mean
	// every kind: a wildcard-only program has no domains to show.
	for _, want := range []string{"wildcard", "all"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("the tools never asked for kind=%s (saw %v)", want, kinds)
		}
	}
}

func TestWrongPasswordIsReported(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "wrong", "test", false)
	got := session(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"huntdeck_status","arguments":{}}}`)
	var res struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	if err := json.Unmarshal(got["1"], &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "sign in") {
		t.Errorf("a wrong password = %s", got["1"])
	}
}

// A read-only server offers only the look-ups, and refuses the write even
// if a client asks for it by name.
func TestReadOnlyHidesPoll(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", true)
	got := session(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"huntdeck_poll","arguments":{"platform":"hackerone"}}}`,
	)
	if strings.Contains(string(got["1"]), "huntdeck_poll") {
		t.Error("a read-only server still lists the poll tool")
	}
	var res struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	if err := json.Unmarshal(got["2"], &res); err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "read-only") {
		t.Errorf("poll on a read-only server = %s", got["2"])
	}
}

// The poll tool asks at most once a minute, however often an agent calls
// it: a loop here would be a load test on the platforms.
func TestPollCooldown(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	clock := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	poll := func(id string) string {
		req := `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"huntdeck_poll","arguments":{"platform":"hackerone"}}}`
		return string(session(t, s, req)[id])
	}
	if first := poll("1"); strings.Contains(first, "isError") {
		t.Fatalf("the first poll was refused: %s", first)
	}
	second := poll("2")
	if !strings.Contains(second, "isError") || !strings.Contains(second, "wait") {
		t.Errorf("a second poll in the same minute = %s", second)
	}
	clock = clock.Add(2 * time.Minute)
	if third := poll("3"); strings.Contains(third, "isError") {
		t.Errorf("a poll after the cooldown was refused: %s", third)
	}
}

// A session gets callBudget calls a minute; the next one is told to wait,
// and the window frees up again by itself.
func TestCallBudget(t *testing.T) {
	srv := instance(t, "pw")
	s := New(srv.URL, "pw", "test", false)
	clock := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	for i := 0; i < callBudget; i++ {
		if _, err := s.callTool(context.Background(), "huntdeck_status", nil); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if _, err := s.callTool(context.Background(), "huntdeck_status", nil); err == nil || !strings.Contains(err.Error(), "too many calls") {
		t.Errorf("the 121st call was allowed: %v", err)
	}
	clock = clock.Add(callWindow + time.Second)
	if _, err := s.callTool(context.Background(), "huntdeck_status", nil); err != nil {
		t.Errorf("after the window: %v", err)
	}
}
