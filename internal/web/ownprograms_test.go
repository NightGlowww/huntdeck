package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A program that isn't on a supported platform is added, edited and
// removed by hand, and shows wherever a platform's would.
func TestOwnPrograms(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	post := func(path string, form url.Values) (*http.Response, string) {
		t.Helper()
		return e.do(c, http.MethodPost, path, form, nil)
	}
	saved := func(res *http.Response, body string) string {
		t.Helper()
		if res.StatusCode != http.StatusSeeOther {
			t.Errorf("POST %s = %d: %.300s", res.Request.URL.Path, res.StatusCode, body)
		}
		return res.Header.Get("Location")
	}
	form := url.Values{"name": {"Initech Bug Bounty"}, "url": {"https://initech.example/security"}, "state": {"open"},
		"paying": {"1"}, "currency": {"USD"}, "bounty_min": {"100"}, "bounty_max": {"20,000"},
		"in_scope": {"*.initech.example\napi.initech.net\n"}, "out_scope": {"status.initech.example"},
		"policy": {"No **DoS**."}}

	res, body := e.do(c, http.MethodGet, "/programs/independent/new", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<form method="post" action="/programs/independent/new"`) ||
		!strings.Contains(body, "Add program</button>") || !strings.Contains(body, `<h1 class="m-heading">Add a program</h1>`) {
		t.Fatalf("new form = %d", res.StatusCode)
	}

	// Mistakes come back with the form as typed.
	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("url", "javascript:alert(1)")
	if res, body = post("/programs/independent/new", bad); res.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "should be a web address") || !strings.Contains(body, "api.initech.net") {
		t.Errorf("bad URL = %d", res.StatusCode)
	}

	if got := saved(post("/programs/independent/new", form)); got != "/programs/independent/initech-bug-bounty?msg=program-saved" {
		t.Fatalf("add went to %q", got)
	}
	// The same name again gets its own handle.
	if got := saved(post("/programs/independent/new", form)); got != "/programs/independent/initech-bug-bounty-2?msg=program-saved" {
		t.Errorf("second add went to %q", got)
	}

	pages := []struct {
		path      string
		want, not []string
	}{
		{"/programs/independent/initech-bug-bounty", []string{"Initech Bug Bounty", "added by hand", `href="/programs/independent/initech-bug-bounty/edit"`,
			"Open program page", "$100 – $20,000", ">Not scored</span>",
			// The Overview shows what was entered.
			`<h2>In scope <span class="count">2</span></h2>`, "and 1 out of scope →", "2 in scope · 1 wildcard · 1 domain", "<h2>Rules and notes</h2>",
			"No <strong>DoS</strong>.", ">initech.example</a>"}, []string{"Open on Independent", "Before you test"}},
		{"/programs/independent/initech-bug-bounty?tab=scope", []string{"*.initech.example", "api.initech.net"}, nil},
		{"/programs/independent/initech-bug-bounty?tab=scope&show=out", []string{"status.initech.example"}, nil},
		{"/programs/independent/initech-bug-bounty?tab=policy", []string{"No <strong>DoS</strong>."}, nil},
		{"/programs?platform=independent", []string{"2 programs</span>", `<span class="fl"><span class="ptag pf-independent" title="Independent">IND</span>Independent</span><em>2</em>`}, nil},
		{"/programs", []string{`href="/programs/independent/new"`, "Add program"}, nil},
		{"/", []string{`title="Independent">IND</a>`}, nil}, // the hunt board's fresh targets narrow to it too
		{"/targets?platform=independent&kind=domain", []string{"api.initech.net"}, nil},
		{"/match?q=www.initech.example", []string{"in scope", "Initech Bug Bounty"}, nil},
		{"/match?q=status.initech.example", []string{"out of scope"}, nil},
		{"/api/v1/programs/independent/initech-bug-bounty", []string{`"platform": "independent"`, `"*.initech.example"`}, nil},
	}
	check := func() {
		t.Helper()
		for _, p := range pages {
			res, body := e.do(c, http.MethodGet, p.path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Errorf("GET %s = %d", p.path, res.StatusCode)
				continue
			}
			for _, w := range p.want {
				if !strings.Contains(body, w) {
					t.Errorf("%s lacks %s", p.path, w)
				}
			}
			for _, n := range p.not {
				if strings.Contains(body, n) {
					t.Errorf("%s has %s", p.path, n)
				}
			}
		}
	}
	check()

	// Nor a Stats tab: an old or made-up link goes to the Overview.
	if res, _ := e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty?tab=stats", nil, nil); res.StatusCode != http.StatusMovedPermanently ||
		res.Header.Get("Location") != "/programs/independent/initech-bug-bounty" {
		t.Errorf("stats tab = %d to %q", res.StatusCode, res.Header.Get("Location"))
	}

	// HuntDeck has no stats to score them by.
	d, err := e.store.Program(context.Background(), store.Independent, "initech-bug-bounty")
	if err != nil || d == nil || d.Hunt != nil {
		t.Errorf("program = %+v, %v; want no HuntScore", d, err)
	}

	// The edit form holds what was saved; saving it again changes nothing.
	res, body = e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty/edit", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, ">*.initech.example\napi.initech.net</textarea>") ||
		!strings.Contains(body, `name="bounty_max" value="20000"`) || !strings.Contains(body, "No **DoS**.</textarea>") {
		t.Fatalf("edit form = %d", res.StatusCode)
	}
	edit := url.Values{}
	for k, v := range form {
		edit[k] = v
	}
	edit.Set("in_scope", "*.initech.example\napi.initech.net\nhttps://portal.initech.example/")
	edit.Set("out_scope", "")
	edit.Set("policy", "")
	edit.Set("bounty_max", "") // only the lowest known: "from $100"
	if got := saved(post("/programs/independent/initech-bug-bounty/edit", edit)); got != "/programs/independent/initech-bug-bounty?msg=program-saved" {
		t.Fatalf("edit went to %q", got)
	}
	// The edit shows as changes, as a platform's news would.
	_, body = e.do(c, http.MethodGet, "/changes?platform=independent&group=scope&since=24h", nil, nil)
	for _, w := range []string{"https://portal.initech.example/", "status.initech.example", "2 changes</span>"} {
		if !strings.Contains(body, w) {
			t.Errorf("changes lack %s", w)
		}
	}
	if _, body = e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty", nil, nil); !strings.Contains(body, `<span class="upto">from</span> $100`) {
		t.Error("a lowest bounty alone isn't shown as from $100")
	}
	if _, body = e.do(c, http.MethodGet, "/targets?platform=independent&kind=domain", nil, nil); !strings.Contains(body, `<span class="upto">from</span> <span class="amt">$100</span>`) {
		t.Error("Targets doesn't show from $100")
	}
	if _, body = e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty?tab=policy", nil, nil); strings.Contains(body, "DoS") {
		t.Error("cleared rules still shown")
	}

	// Removing one keeps the other.
	if got := saved(post("/programs/independent/initech-bug-bounty-2/remove", url.Values{})); got != "/programs?platform=independent&msg=program-removed" {
		t.Fatalf("remove went to %q", got)
	}
	pages = pages[4:5] // the Programs list
	pages[0].want = []string{"1 program</span>"}
	check()
	if res, _ = e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty-2/edit", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("edit of a removed program = %d", res.StatusCode)
	}
	if res, body = e.do(c, http.MethodGet, "/programs/independent/initech-bug-bounty-2", nil, nil); !strings.Contains(body, "You removed this program") {
		t.Errorf("removed program's page = %d", res.StatusCode)
	}
	// A program on a platform can't be edited here.
	if res, _ = e.do(c, http.MethodGet, "/programs/independent/acme/edit", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("edit of a platform's program = %d", res.StatusCode)
	}
	// Independent isn't a platform to poll, show stats for, or alert on.
	// Quoted through T, the note's apostrophe is escaped; match its opening.
	if _, body = e.do(c, http.MethodGet, "/stats", nil, nil); strings.Contains(body, "pf-independent") || !strings.Contains(body, "Public <em>1</em>") ||
		!strings.Contains(body, "Programs added by hand") {
		t.Error("Stats shows Independent, or counts it among public programs")
	}
	if res, _ = e.do(c, http.MethodGet, "/stats/independent", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("/stats/independent = %d", res.StatusCode)
	}
	if _, body = e.do(c, http.MethodGet, "/settings", nil, nil); strings.Contains(body, `name="platform" value="independent"`) {
		t.Error("Settings offers Independent as a platform")
	}
}

// A read-only copy shows the form but can't save it, and keeps the
// owner's settings (seeds, removing) to itself.
func TestOwnProgramsReadOnly(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.SaveOwn = nil })
	c := e.client()
	if err := e.syncer.SaveOwn(context.Background(), []independent.Program{{Program: platform.Program{Handle: "initech", Name: "Initech",
		URL: "https://initech.example/", State: platform.StateOpen}}}); err != nil {
		t.Fatal(err)
	}
	if _, body := e.do(c, http.MethodGet, "/programs/independent/new", nil, nil); !strings.Contains(body, "this form adds programs") ||
		!strings.Contains(body, "Here it can't be saved.") || strings.Contains(body, "Include in seeds") {
		t.Error("the add form doesn't say it can't be saved, or offers seeds")
	}
	_, body := e.do(c, http.MethodGet, "/programs/independent/initech/edit", nil, nil)
	if !strings.Contains(body, "this form edits a program you added by hand") || strings.Contains(body, "adds programs") ||
		strings.Contains(body, "Include in seeds") || strings.Contains(body, "remove-program") {
		t.Error("the edit form reads as the add form, or offers seeds or removing")
	}
	res, _ := e.do(c, http.MethodPost, "/programs/independent/new", url.Values{"name": {"x"}, "url": {"https://x.example/"}}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("save = %d", res.StatusCode)
	}
}
