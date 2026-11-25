package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

const testToken = "cf-test-token-0123456789"

func testBundle(t *testing.T) *Bundle {
	t.Helper()
	js := []byte("export default { fetch() { return new Response('ok') } }\n")
	sum := sha256.Sum256(js)
	m1, m2 := "CREATE TABLE emails (id TEXT);\n", "CREATE INDEX e ON emails (id);"
	manifest := `{"name":"huntmail","version":"1.2.3","main_module":"index.js",
		"modules":[{"name":"index.js","type":"esm","sha256":"` + hex.EncodeToString(sum[:]) + `"}],
		"compatibility_date":"2026-10-01","compatibility_flags":["nodejs_compat"],"crons":["* * * * *"],
		"observability":{"enabled":true},"d1_binding":"DB","vars":{"DOMAINS":"","FORWARD_TO":"","RETENTION_DAYS":"30"},
		"secrets":["API_KEY","BASE_PATH"],"migrations":["0001_init.sql","0002_more.sql"],
		"migrations_sha256":{"0001_init.sql":"` + sha(m1) + `","0002_more.sql":"` + sha(m2) + `"},"migrations_table":"d1_migrations","workers_dev":true}`
	b, err := Load(fstest.MapFS{
		"manifest.json":            {Data: []byte(manifest)},
		"index.js":                 {Data: js},
		"migrations/0001_init.sql": {Data: []byte(m1)},
		"migrations/0002_more.sql": {Data: []byte(m2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeCF is Cloudflare's API in memory, with one account.
type fakeCF struct {
	mu        sync.Mutex
	calls     []string // "METHOD path"
	refuse    bool
	malformed bool // answers as Cloudflare does a token it can't read
	sub       string
	worker    bool
	vars      map[string]string
	secrets   map[string]string
	dbs       map[string]string // name → uuid
	applied   []string
	catchAll  map[string]any // zone → its rule
	routing   map[string]bool
	upload    map[string]any // the last upload's metadata
	uploadJS  string
	schedules string
}

func newFakeCF() *fakeCF {
	return &fakeCF{sub: "hunter", vars: map[string]string{}, secrets: map[string]string{}, dbs: map[string]string{},
		catchAll: map[string]any{}, routing: map[string]bool{}}
}

func (f *fakeCF) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") && !strings.Contains(c, "SELECT") && !strings.Contains(c, "CREATE TABLE IF NOT EXISTS d1_migrations") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	fail := func(status, code int, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": code, "message": msg}}})
	}
	if f.malformed {
		fail(http.StatusBadRequest, 6003, "Invalid request headers")
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testToken || f.refuse {
		fail(http.StatusForbidden, 10000, "Authentication error")
		return
	}
	body, _ := io.ReadAll(r.Body)
	call := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	var q struct {
		SQL string `json:"sql"`
	}
	if strings.HasSuffix(r.URL.Path, "/query") {
		_ = json.Unmarshal(body, &q)
		call += " " + q.SQL
	}
	f.calls = append(f.calls, call)
	const script = "/accounts/acct1/workers/scripts/huntmail"
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/zones":
		switch name := r.URL.Query().Get("name"); name {
		case "example.com", "example.net":
			ok([]any{map[string]any{"id": "z-" + name, "name": name, "account": map[string]any{"id": "acct1"}}})
		default:
			ok([]any{})
		}
	case strings.HasSuffix(p, "/email/routing/rules/catch_all"):
		zone := strings.Split(p, "/")[2]
		if r.Method == "PUT" {
			var rule map[string]any
			_ = json.Unmarshal(body, &rule)
			f.catchAll[zone] = rule
			ok(rule)
			return
		}
		if rule, has := f.catchAll[zone]; has {
			ok(rule)
		} else {
			ok(map[string]any{"enabled": false, "actions": []any{}})
		}
	case r.Method == "GET" && strings.HasSuffix(p, "/email/routing"):
		ok(map[string]any{"enabled": f.routing[strings.Split(p, "/")[2]], "status": map[bool]string{true: "ready", false: "unconfigured"}[f.routing[strings.Split(p, "/")[2]]]})
	case r.Method == "POST" && strings.HasSuffix(p, "/email/routing/dns"):
		f.routing[strings.Split(p, "/")[2]] = true
		ok(map[string]any{})
	case r.Method == "GET" && p == script+"/settings":
		if !f.worker {
			fail(http.StatusNotFound, 10007, "This Worker does not exist on your account.")
			return
		}
		bs := []any{map[string]any{"type": "d1", "name": "DB", "id": f.dbs["huntmail"]}}
		for k, v := range f.vars {
			bs = append(bs, map[string]any{"type": "plain_text", "name": k, "text": v})
		}
		for k := range f.secrets {
			bs = append(bs, map[string]any{"type": "secret_text", "name": k})
		}
		ok(map[string]any{"bindings": bs})
	case r.Method == "GET" && p == script+"/secrets":
		var out []any
		for k := range f.secrets {
			out = append(out, map[string]any{"name": k, "type": "secret_text"})
		}
		ok(out)
	case r.Method == "PUT" && p == script+"/secrets":
		var s map[string]string
		_ = json.Unmarshal(body, &s)
		f.secrets[s["name"]] = s["text"]
		ok(map[string]any{"name": s["name"]})
	case r.Method == "GET" && p == "/accounts/acct1/workers/subdomain":
		ok(map[string]any{"subdomain": f.sub})
	case r.Method == "GET" && p == "/accounts/acct1/d1/database":
		var out []any
		if id, has := f.dbs[r.URL.Query().Get("name")]; has {
			out = append(out, map[string]any{"uuid": id, "name": r.URL.Query().Get("name")})
		}
		ok(out)
	case r.Method == "POST" && p == "/accounts/acct1/d1/database":
		var d map[string]string
		_ = json.Unmarshal(body, &d)
		f.dbs[d["name"]] = "db-uuid-1"
		ok(map[string]any{"uuid": "db-uuid-1", "name": d["name"]})
	case r.Method == "POST" && p == "/accounts/acct1/d1/database/db-uuid-1/query":
		var rows []any
		if strings.HasPrefix(q.SQL, "SELECT name FROM d1_migrations") {
			for _, n := range f.applied {
				rows = append(rows, map[string]any{"name": n})
			}
		}
		if i := strings.Index(q.SQL, "INSERT INTO d1_migrations (name) VALUES ('"); i >= 0 {
			f.applied = append(f.applied, strings.TrimSuffix(q.SQL[i+len("INSERT INTO d1_migrations (name) VALUES ('"):], "');"))
		}
		ok([]any{map[string]any{"results": rows, "success": true}})
	case r.Method == "PUT" && p == script:
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(part)
			switch part.FormName() {
			case "metadata":
				f.upload = map[string]any{}
				_ = json.Unmarshal(b, &f.upload)
			case "index.js":
				if part.Header.Get("Content-Type") == "application/javascript+module" {
					f.uploadJS = string(b)
				}
			}
		}
		vars := map[string]string{}
		for _, b := range f.upload["bindings"].([]any) {
			if b := b.(map[string]any); b["type"] == "plain_text" {
				text, has := b["text"].(string)
				if !has { // as Cloudflare answers
					fail(http.StatusBadRequest, 10021, "invalid or missing text property for binding "+b["name"].(string))
					return
				}
				vars[b["name"].(string)] = text
			}
		}
		f.worker, f.vars = true, vars
		ok(map[string]any{"id": "huntmail"})
	case r.Method == "PUT" && p == script+"/schedules":
		f.schedules = string(body)
		ok(map[string]any{})
	case r.Method == "POST" && p == script+"/subdomain":
		ok(map[string]any{"enabled": true})
	default:
		fail(http.StatusNotFound, 7003, "No route for that URI")
	}
}

type env struct {
	cf     *fakeCF
	opts   Options
	health *httptest.Server
	steps  []Step
}

func newEnv(t *testing.T) *env {
	e := &env{cf: newFakeCF()}
	api := httptest.NewServer(e.cf)
	t.Cleanup(api.Close)
	e.health = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the Cloudflare token went to HuntMail")
		}
		if !strings.HasSuffix(r.URL.Path, "/health") {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(e.health.Close)
	hc := httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 1000, MaxAttempts: 1})
	e.opts = Options{Token: testToken, Domains: []string{"example.com"}, API: api.URL, HTTP: hc, Health: hc, HealthAt: e.health.URL,
		Sleep: func(context.Context, time.Duration) error { return nil }, Progress: func(s []Step) { e.steps = s }}
	return e
}

func TestRunFresh(t *testing.T) {
	e := newEnv(t)
	res, err := Run(context.Background(), testBundle(t), e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.2.3" || res.Origin != "https://huntmail.hunter.workers.dev" || !res.Healthy {
		t.Errorf("result: %+v", res)
	}
	if len(res.Key) != 64 || e.cf.secrets["API_KEY"] != res.Key || res.URL != res.Origin+"/"+e.cf.secrets["BASE_PATH"] || len(e.cf.secrets["BASE_PATH"]) != 32 {
		t.Errorf("the key and address made: %+v", res)
	}
	if strings.Join(e.cf.applied, ",") != "0001_init.sql,0002_more.sql" {
		t.Errorf("migrations: %v", e.cf.applied)
	}
	m := e.cf.upload
	if m["main_module"] != "index.js" || m["compatibility_date"] != "2026-10-01" || !strings.Contains(e.cf.uploadJS, "export default") {
		t.Errorf("upload: %v", m)
	}
	if kb, _ := json.Marshal(m["keep_bindings"]); string(kb) != `["secret_text"]` {
		t.Errorf("keep_bindings: %s", kb)
	}
	if e.cf.vars["DOMAINS"] != "example.com" || e.cf.vars["RETENTION_DAYS"] != "30" {
		t.Errorf("vars: %v", e.cf.vars)
	}
	if e.cf.schedules != `[{"cron":"* * * * *"}]` || !e.cf.routing["z-example.com"] {
		t.Errorf("schedule %s, routing %v", e.cf.schedules, e.cf.routing)
	}
	if rule, _ := json.Marshal(e.cf.catchAll["z-example.com"]); !strings.Contains(string(rule), `"actions":[{"type":"worker","value":["huntmail"]}]`) {
		t.Errorf("catch-all: %s", rule)
	}
	for _, s := range e.steps {
		if s.State != StepDone {
			t.Errorf("step %q: %s %s", s.Title, s.State, s.Detail)
		}
	}

	// Again, with the address HuntDeck saved and one more domain: the key
	// stays, the user's settings stay, nothing is applied twice.
	e.cf.vars["RETENTION_DAYS"] = "7"
	e.opts.KnownURL, e.opts.Domains = res.URL, []string{"example.com", "example.net"}
	key := res.Key
	res, err = Run(context.Background(), testBundle(t), e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "" || res.URL != "" || e.cf.secrets["API_KEY"] != key {
		t.Errorf("the key was replaced: %+v", res)
	}
	if e.cf.vars["DOMAINS"] != "example.com,example.net" || e.cf.vars["RETENTION_DAYS"] != "7" || len(e.cf.applied) != 2 || len(e.cf.dbs) != 1 {
		t.Errorf("rerun: vars %v, migrations %v, databases %v", e.cf.vars, e.cf.applied, e.cf.dbs)
	}
	if strings.Join(res.Domains, ",") != "example.com,example.net" {
		t.Errorf("domains: %v", res.Domains)
	}
}

// Anything that needs the user's say stops before the first change.
func TestRunStopsFirst(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(*fakeCF, *Options)
		want    func(error) bool
		text    string
	}{
		"a catch-all forwards": {
			prepare: func(f *fakeCF, _ *Options) {
				f.catchAll["z-example.com"] = map[string]any{"enabled": true, "actions": []any{map[string]any{"type": "forward", "value": []string{"me@example.org"}}}}
			},
			want: func(err error) bool { var c *CatchAllError; return errors.As(err, &c) },
			text: "forward to me@example.org",
		},
		"a HuntMail whose key HuntDeck lacks": {
			prepare: func(f *fakeCF, _ *Options) { f.worker, f.secrets["API_KEY"], f.secrets["BASE_PATH"] = true, "k", "b" },
			want:    func(err error) bool { return errors.Is(err, ErrKeyUnknown) },
		},
		"another account's HuntMail address": {
			prepare: func(f *fakeCF, o *Options) {
				f.worker, f.secrets["API_KEY"], f.secrets["BASE_PATH"] = true, "k", "b"
				o.KnownURL = "https://huntmail.someoneelse.workers.dev/b"
			},
			want: func(err error) bool { return errors.Is(err, ErrKeyUnknown) },
		},
		"a domain not on the account": {
			prepare: func(_ *fakeCF, o *Options) { o.Domains = []string{"example.org"} },
			want:    func(err error) bool { return err != nil },
			text:    "example.org isn't on this Cloudflare account",
		},
		"a refused token": {
			prepare: func(f *fakeCF, _ *Options) { f.refuse = true },
			want:    func(err error) bool { var c *CFError; return errors.As(err, &c) },
			text:    "the token was refused, or it lacks the Zone Read permission",
		},
		"a malformed token": {
			prepare: func(f *fakeCF, _ *Options) { f.malformed = true },
			want:    func(err error) bool { var c *CFError; return errors.As(err, &c) },
			text:    "the token was refused",
		},
		"no workers.dev subdomain": {
			prepare: func(f *fakeCF, _ *Options) { f.sub = "" },
			want:    func(err error) bool { return err != nil },
			text:    "no workers.dev subdomain",
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			tc.prepare(e.cf, &e.opts)
			_, err := Run(context.Background(), testBundle(t), e.opts)
			if !tc.want(err) || !strings.Contains(errText(err), tc.text) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Error("the token is in the error")
			}
			if w := e.cf.writes(); len(w) != 0 {
				t.Errorf("changed the account before stopping: %v", w)
			}
			if last := e.steps[len(e.steps)-1]; last.State != StepFailed || last.Detail != err.Error() {
				t.Errorf("the failed step: %+v", last)
			}
		})
	}
}

// Told to, it replaces a catch-all and makes a new key.
func TestRunReplaces(t *testing.T) {
	e := newEnv(t)
	e.cf.catchAll["z-example.com"] = map[string]any{"enabled": true, "actions": []any{map[string]any{"type": "forward", "value": []string{"me@example.org"}}}}
	e.cf.worker, e.cf.secrets["API_KEY"], e.cf.secrets["BASE_PATH"] = true, "old-key", "old-base"
	e.cf.dbs["huntmail"] = "db-uuid-1"
	e.opts.ReplaceCatchAll, e.opts.NewKey = true, true
	res, err := Run(context.Background(), testBundle(t), e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Key == "" || e.cf.secrets["API_KEY"] != res.Key || e.cf.secrets["BASE_PATH"] == "old-base" {
		t.Errorf("new key: %+v %v", res, e.cf.secrets)
	}
	if rule, _ := json.Marshal(e.cf.catchAll["z-example.com"]); !strings.Contains(string(rule), `"worker"`) {
		t.Errorf("catch-all: %s", rule)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestLoad(t *testing.T) {
	if b, err := Embedded(); err != nil || b.Version() == "" || len(b.Migrations) == 0 {
		t.Fatalf("the bundle built in: %v", err)
	}
	good := testBundle(t)
	if good.Version() != "1.2.3" || good.migrations["0002_more.sql"] == "" {
		t.Errorf("loaded: %+v", good.Manifest)
	}
	if _, err := Load(fstest.MapFS{}); !errors.Is(err, ErrNoBundle) {
		t.Errorf("no manifest: %v", err)
	}
	for name, manifest := range map[string]string{
		"a changed module": `{"version":"1","main_module":"index.js","compatibility_date":"2026-10-01","d1_binding":"DB","secrets":["API_KEY","BASE_PATH"],
			"modules":[{"name":"index.js","type":"esm","sha256":"00"}]}`,
		"a path in a module's name": `{"version":"1","main_module":"../x.js","compatibility_date":"2026-10-01","d1_binding":"DB","secrets":["API_KEY","BASE_PATH"],
			"modules":[{"name":"../x.js","type":"esm","sha256":"00"}]}`,
		"no base path secret": `{"version":"1","main_module":"index.js","compatibility_date":"2026-10-01","d1_binding":"DB","secrets":["API_KEY"],
			"modules":[{"name":"index.js","type":"esm","sha256":"00"}]}`,
		"a changed migration": `{"version":"1","main_module":"index.js","compatibility_date":"2026-10-01","d1_binding":"DB","secrets":["API_KEY","BASE_PATH"],
			"modules":[{"name":"index.js","type":"esm","sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}],"migrations":["0001_a.sql"],
			"migrations_sha256":{"0001_a.sql":"00"}}`,
		"a quote in a migration's name": `{"version":"1","main_module":"index.js","compatibility_date":"2026-10-01","d1_binding":"DB","secrets":["API_KEY","BASE_PATH"],
			"modules":[{"name":"index.js","type":"esm","sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}],"migrations":["0001_a');--.sql"]}`,
	} {
		_, err := Load(fstest.MapFS{"manifest.json": {Data: []byte(manifest)}, "index.js": {Data: []byte("abc")}, "migrations/0001_a.sql": {Data: []byte("x")}})
		if err == nil || errors.Is(err, ErrNoBundle) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckDomains(t *testing.T) {
	got, err := CheckDomains([]string{" Example.COM. ", "example.com", "", "mail.example.net"})
	if err != nil || strings.Join(got, ",") != "example.com,mail.example.net" {
		t.Errorf("%v %v", got, err)
	}
	for _, bad := range []string{"example", "exa mple.com", "-x.example.com", "a/b.com"} {
		if _, err := CheckDomains([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
