package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func boardChange(id, program int64, kind, field, oldV, newV string) store.BoardChange {
	c := store.BoardChange{}
	c.ID, c.ProgramID, c.Kind, c.Field, c.OldValue, c.NewValue = id, program, kind, field, oldV, newV
	c.OccurredAt = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Add(-time.Duration(id) * time.Hour)
	c.Program = store.ProgramRef{ID: program, Name: "p" + string(rune('0'+program))}
	return c
}

func TestBoardDiffs(t *testing.T) {
	// Newest first, as BoardChanges returns them.
	changes := []store.BoardChange{
		boardChange(1, 1, store.ChangeScopeAdded, "in_scope", "", "a.example.com"),
		boardChange(2, 2, store.ChangeScopeRemoved, "in_scope", "old.example.net", ""),
		boardChange(3, 1, store.ChangeScopeAdded, "in_scope", "", "b.example.com"),
		boardChange(4, 1, store.ChangeScopeAdded, "out_of_scope", "", "c.example.com"),
		boardChange(5, 1, store.ChangeScopeAdded, "in_scope", "", "d.example.com"),
		boardChange(6, 3, store.ChangeProgramAdded, "visibility", "", "public"),
		boardChange(7, 2, store.ChangeScopeUpdated, "asset_type", "url", "other"),
	}
	changes[6].Identifier = "/self-service-*"
	diffs, programs := boardDiffs(changes, 2)
	if len(diffs) != 2 || programs != 3 {
		t.Fatalf("got %d cards of %d programs, want 2 of 3", len(diffs), programs)
	}
	p1, p2 := diffs[0], diffs[1]
	if p1.Program.ID != 1 || p1.Added != 3 || p1.Edited != 1 || len(p1.Lines) != boardDiffRows || p1.More != 1 {
		t.Errorf("program 1 card = %+v", p1)
	}
	if p1.Lines[2] != (boardLine{Class: "mod", Text: "c.example.com  (out of scope)"}) {
		t.Errorf("out-of-scope addition = %+v", p1.Lines[2])
	}
	if p2.Removed != 1 || p2.Edited != 1 || p2.Lines[0].Class != "del" || p2.Lines[1].Text != "/self-service-*  asset type url → other" {
		t.Errorf("program 2 card = %+v", p2)
	}
}

func TestStatusOf(t *testing.T) {
	open, paused := platform.StateOpen, platform.StatePaused
	// Newest first: program 1 flips four times, 2 reopens, 3 pauses, 4 pauses then reopens.
	states := []store.BoardChange{
		boardChange(1, 1, store.ChangeStateChanged, "state", open, paused),
		boardChange(2, 2, store.ChangeStateChanged, "state", paused, open),
		boardChange(3, 1, store.ChangeStateChanged, "state", paused, open),
		boardChange(4, 3, store.ChangeStateChanged, "state", open, paused),
		boardChange(5, 4, store.ChangeStateChanged, "state", paused, open),
		boardChange(6, 1, store.ChangeStateChanged, "state", open, paused),
		boardChange(7, 4, store.ChangeStateChanged, "state", open, paused),
		boardChange(8, 1, store.ChangeStateChanged, "state", paused, open),
	}
	// The Programs list counts 2 back open, not 4: its flips around the
	// reopen (some before this week) make it flip-flop.
	st := statusOf(states, []store.ProgramListItem{{ProgramRef: store.ProgramRef{ID: 2}}})
	ids := func(ps []store.ProgramRef) (out []int64) {
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	if got := ids(st.Reopened); len(got) != 1 || got[0] != 2 {
		t.Errorf("reopened = %v, want [2]", got)
	}
	if got := ids(st.Paused); len(got) != 1 || got[0] != 3 {
		t.Errorf("paused = %v, want [3]", got)
	}
	if len(st.FlipFloppers) != 2 || st.FlipFloppers[0].Program.ID != 1 || st.FlipFloppers[0].Flips != 4 || st.FlipFloppers[1].Program.ID != 4 {
		t.Errorf("flip-floppers = %+v", st.FlipFloppers)
	}
}

func TestBoardFilterURLs(t *testing.T) {
	tests := []struct {
		name, query string
		url         func(boardFilter) string
		want        string
	}{
		{"defaults", "", func(f boardFilter) string { return f.KindURL("") }, "/"},
		{"kind", "", func(f boardFilter) string { return f.KindURL(scope.KindWildcard) }, "/?kind=wildcard"},
		{"bounty off", "kind=domains", func(f boardFilter) string { return f.BountyURL() }, "/?bounty=0&kind=domain"},
		{"bounty back on", "bounty=0", func(f boardFilter) string { return f.BountyURL() }, "/"},
		{"bad added", "added=99d", func(f boardFilter) string { return f.AddedURL(f.Added) }, "/"},
		{"best first", "added=7d", func(f boardFilter) string { return f.SortURL("score") }, "/?added=7d&sort=score"},
		{"newest first is the default", "sort=score", func(f boardFilter) string { return f.SortURL("new") }, "/"},
		{"highest bounty first", "kind=wildcard", func(f boardFilter) string { return f.SortURL("bounty") }, "/?kind=wildcard&sort=bounty"},
		{"the current sort again turns it around", "sort=score", func(f boardFilter) string { return f.SortURL("score") }, "/?rev=1&sort=score"},
		{"and back", "sort=score&rev=1", func(f boardFilter) string { return f.SortURL("score") }, "/?sort=score"},
		{"oldest first", "", func(f boardFilter) string { return f.SortURL("new") }, "/?rev=1"},
		{"a new sort starts the usual way", "rev=1", func(f boardFilter) string { return f.SortURL("bounty") }, "/?sort=bounty"},
		{"filters keep the direction", "sort=bounty&rev=1", func(f boardFilter) string { return f.KindURL(scope.KindDomain) }, "/?kind=domain&rev=1&sort=bounty"},
		{"bad sort", "sort=random", func(f boardFilter) string { return f.KindURL("") }, "/"},
		{"targets page", "kind=wildcard&added=7d", func(f boardFilter) string { return f.TargetsURL() },
			"/targets?added=7d&bounty=1&kind=wildcard&sort=best"},
		{"targets page, every kind", "", func(f boardFilter) string { return f.TargetsURL() },
			"/targets?added=14d&bounty=1&kind=all&sort=best"},
		{"apps", "kind=apps", func(f boardFilter) string { return f.KindURL(scope.KindIP) }, "/?kind=ip%2Capp"},
		// Kind chips combine, in the chips' order, and each turns itself off.
		{"another kind joins", "kind=url", func(f boardFilter) string { return f.KindURL(scope.KindWildcard) }, "/?kind=wildcard%2Curl"},
		{"a chosen kind leaves", "kind=wildcard,url,ip", func(f boardFilter) string { return f.KindURL(scope.KindURL) }, "/?kind=wildcard%2Cip"},
		{"the last one leaves: all", "kind=ip", func(f boardFilter) string { return f.KindURL(scope.KindIP) }, "/"},
		{"all clears them", "kind=ip,url", func(f boardFilter) string { return f.KindURL("") }, "/"},
		{"unknown kinds go", "kind=root,nope,cidr", func(f boardFilter) string { return f.AddedURL("7d") }, "/?added=7d&kind=cidr"},
		{"no targets page for several kinds", "kind=ip,url", func(f boardFilter) string { return f.TargetsURL() }, ""},
		{"nor excluded words", "not=staging", func(f boardFilter) string { return f.TargetsURL() }, ""},
		{"filters keep the excluded words", "not=staging", func(f boardFilter) string { return f.KindURL(scope.KindIP) }, "/?kind=ip&not=staging"},
		{"targets page for apps", "kind=app", func(f boardFilter) string { return f.TargetsURL() },
			"/targets?added=14d&bounty=1&kind=app&sort=best"},
	}
	for _, tt := range tests {
		q, _ := url.ParseQuery(tt.query)
		if got := tt.url(boardFilterFrom(q)); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestHuntBoard(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	e.fake.SetScope("evil", platform.ScopeEntry{PlatformID: "6", InScope: true, AssetType: scope.AssetWildcard,
		Identifier: "*.new.evil.example", BountyEligible: true},
		platform.ScopeEntry{PlatformID: "7", InScope: true, AssetType: scope.AssetAndroid, Identifier: "com.example.evil", BountyEligible: true},
		platform.ScopeEntry{PlatformID: "8", InScope: true, AssetType: scope.AssetIOS, Identifier: "1234567890", BountyEligible: true})
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}

	_, body := e.do(c, http.MethodGet, "/?bounty=0&kind=wildcards", nil, nil)
	// Program comes first, as on the Programs page.
	if p, tg := strings.Index(body, `<th class="c-prog" data-col-fixed>`), strings.Index(body, `<th class="c-target"`); p < 0 || p > tg {
		t.Errorf("program isn't the first column: program %d, target %d", p, tg)
	}
	// Score sits next to Program, and Total paid after Bounty, as on the Programs page.
	if a, b, p, tp := strings.Index(body, `<th class="c-prog" data-col-fixed>`), strings.Index(body, `<th class="c-score num">`), strings.Index(body, `<th class="c-pay">`), strings.Index(body, `<th class="c-ptotal">`); a < 0 || a >= b || b >= p || p >= tp {
		t.Errorf("fresh targets' columns out of order: program %d, score %d, bounty %d, total paid %d", a, b, p, tp)
	}
	for _, want := range []string{`class="topbar"`, `id="omni"`, `<table class="fixed surface" data-cols="board-targets">`, `data-colpick="board-targets"`, `<input type="checkbox" value="score"> Target score</label>`, `<b class="score">`, `<td class="c-target asset" title="*.new.evil.example"><div class="tv-row"><div class="tv">*.new.evil.example</div>`, `<a class="sort on" href="/?bounty=0&amp;kind=wildcard&amp;rev=1" title="Newest first">Added</a>`, `href="/?bounty=0&amp;kind=wildcard&amp;sort=score"`, `href="/?bounty=0&amp;kind=wildcard&amp;sort=bounty"`, "newest first</span>",
		`data-copy-text="*.new.evil.example"`, `<th class="c-pay"`, `<td class="c-pay" title="Not bounty-eligible"><div class="pay"><span class="none" title="Pays no bounties">—</span>`,
		`<td class="c-ptotal"><span class="zero" title="Pays no bounties">—</span></td>`,
		`<a class="fchip" href="/?bounty=0">All</a>`, `<a class="fchip on" href="/?bounty=0" aria-current="true" title="Show or hide wildcards; pick several to combine them">Wildcards</a>`, `<a class="fchip" href="/?bounty=0&amp;kind=wildcard%2Curl"`, "<h2>Scope diffs</h2>", "&lt;script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("hunt board lacks %q", want)
		}
	}
	topbar := body[strings.Index(body, `class="topbar"`):strings.Index(body, `<div class="page">`)]
	for _, want := range []string{`<span class="updated" title="The latest poll finished `, ">Updated ", "data-theme-toggle", "data-board-edit",
		`<div class="tmenu" data-theme-menu hidden>`, `name="tm-mode" value="system"`, `name="tm-palette" value="graphite"`, `<span class="tm-sw" data-palette="nord"`} {
		if !strings.Contains(topbar, want) {
			t.Errorf("the top bar lacks %q", want)
		}
	}
	if strings.Contains(body, "demo-banner") || strings.Contains(body, "data-read-only") {
		t.Error("a full install shows the demo banner")
	}
	if strings.Contains(body, "Export .txt") || strings.Contains(body, "download=1") || strings.Contains(body, `class="page-head"`) {
		t.Error("the hunt board still has its header or an export")
	}
	for _, box := range []string{"totals", "fresh", "diffs", "picks"} {
		if !strings.Contains(body, `data-box="`+box+`"`) {
			t.Errorf("hunt board lacks the %s box", box)
		}
	}
	if !regexp.MustCompile(`<script src="/static/board\.js\?v=[0-9a-f]{12}"></script>`).MatchString(body) ||
		!regexp.MustCompile(`href="/static/styles\.css\?v=[0-9a-f]{12}"`).MatchString(body) {
		t.Error("static files aren't linked with a version")
	}
	// The stylesheets come as one, in order, kept for good at this version.
	v := regexp.MustCompile(`styles\.css\?v=([0-9a-f]{12})`).FindStringSubmatch(body)[1]
	res, css := e.do(c, http.MethodGet, "/static/styles.css?v="+v, nil, nil)
	theme, system := strings.Index(css, "--bg"), strings.Index(css, ".panel-foot")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") || theme < 0 || system < theme ||
		!strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("styles.css: %d %q %q, theme at %d, system at %d", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Cache-Control"), theme, system)
	}
	if res, _ := e.do(c, http.MethodGet, "/static/app.js", nil, nil); strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Error("a static file asked for without the version is kept for good")
	}
	if strings.Contains(body, `style="`) {
		t.Error("hunt board uses an inline style, which the CSP blocks")
	}
	if strings.Count(body, `role="search"`) != 1 || strings.Contains(body, "<script>alert") || strings.Contains(body, "%!") {
		t.Error("hunt board has more than the one search bar, unescaped input or a formatting error")
	}
	_, body = e.do(c, http.MethodGet, "/?bounty=0&kind=apps", nil, nil)
	for _, want := range []string{`<a class="fchip on" href="/?bounty=0" aria-current="true" title="Show or hide apps; pick several to combine them">Apps</a>`, `<span class="kd android">android</span>`, `<span class="kd ios">iOS</span>`,
		// The open button goes to the program on its platform, never the app.
		`class="open-ext" href="https://platform.example/`, `target="_blank" rel="noopener noreferrer nofollow"`, `data-copy-text="com.example.evil"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the board's apps lack %q", want)
		}
	}
	if strings.Contains(body, "play.google.com") || strings.Contains(body, "apps.apple.com") {
		t.Error("the board links to an app store rather than the program's platform")
	}
	if fresh := body[strings.Index(body, `data-box="fresh"`):strings.Index(body, `data-box="diffs"`)]; strings.Contains(fresh, "*.new.evil.example") || !strings.Contains(fresh, "/targets?added=14d&amp;kind=app&amp;sort=best") {
		t.Error("the board's apps list a web target, or don't link to them on Targets")
	}
	fresh := func(path string) string {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		return body[strings.Index(body, `data-box="fresh"`):strings.Index(body, `data-box="diffs"`)]
	}
	// Kinds combine, and excluded words leave out targets or programs.
	if both := fresh("/?bounty=0&kind=wildcard,app"); !strings.Contains(both, `data-copy-text="*.new.evil.example"`) || !strings.Contains(both, `data-copy-text="com.example.evil"`) {
		t.Error("wildcards and apps together don't list both")
	}
	if left := fresh("/?bounty=0&kind=wildcard,app&not=com.example"); !strings.Contains(left, `data-copy-text="*.new.evil.example"`) || strings.Contains(left, `data-copy-text="com.example.evil"`) {
		t.Error("excluding a target's word keeps it, or drops the rest")
	}
	if left := fresh("/?bounty=0&kind=wildcard,app&not=evil"); strings.Contains(left, `data-copy-text=`) || !strings.Contains(left, `name="not" value="evil"`) {
		t.Error("excluding a program's name keeps its targets, or loses the field's value")
	}
	if _, body = e.do(c, http.MethodGet, "/?bounty=0&kind=wildcard", nil, nil); strings.Contains(body[strings.Index(body, `data-box="fresh"`):strings.Index(body, `data-box="diffs"`)], "com.example.evil") {
		t.Error("an app is listed under wildcards")
	}
	if _, body = e.do(c, http.MethodGet, "/", nil, nil); strings.Contains(body, `<td class="c-target asset" title="*.new.evil.example"><div class="tv-row"><div class="tv">*.new.evil.example</div>`) {
		t.Error("the board's default bounty-only list shows a target of a program that pays nothing")
	}
	if !regexp.MustCompile(`<span class="l">Wildcards</span><span class="v">\d+</span><span class="h" title="Added in the last 7 days, as Targets › Added › Last 7 days counts them"><b class="up">\+1</b> this week</span>`).MatchString(body) ||
		strings.Contains(body, `data-box="week"`) {
		t.Error("the totals don't count the week's new wildcard")
	}
	e.do(c, http.MethodPost, "/programs/fake/evil/ignore", url.Values{"ignored": {"1"}}, nil)
	if _, body = e.do(c, http.MethodGet, "/?bounty=0", nil, nil); !regexp.MustCompile(`<span class="l">Wildcards</span><span class="v">\d+</span><span class="h" title="Added in the last 7 days, as Targets › Added › Last 7 days counts them"><b class="none">\+0</b>`).MatchString(body) ||
		strings.Contains(body, "*.new.evil.example") {
		t.Error("the board still shows an ignored program's changes")
	}
	e.do(c, http.MethodPost, "/programs/fake/evil/ignore", url.Values{"ignored": {"0"}}, nil)
	if _, body = e.do(c, http.MethodGet, "/programs", nil, nil); !strings.Contains(body, `class="topbar"`) ||
		!strings.Contains(body, "data-theme-toggle") || strings.Contains(body, "data-board-edit") {
		t.Error("other pages lost the top search bar")
	}
}

// A private program just joined lists a few of its targets as new to you,
// tagged private, with the rest a link away.
func TestHuntBoardNewToYou(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	e.fake.SetPrograms(platform.Program{Handle: "initech", Name: "Initech", URL: "https://platform.example/initech",
		Private: true, State: platform.StateOpen, OffersBounty: true})
	var entries []platform.ScopeEntry
	for i, h := range []string{"a", "b", "c", "d", "e"} {
		entries = append(entries, platform.ScopeEntry{PlatformID: "i" + strconv.Itoa(i), InScope: true, AssetType: scope.AssetURL,
			Identifier: h + ".initech.example", BountyEligible: true})
	}
	e.fake.SetScope("initech", entries...)
	e.fake.SetHistory("initech", platform.ScopeVersion{At: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Entries: entries})
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(c, http.MethodGet, "/", nil, nil)
	fresh := body[strings.Index(body, `id="fresh"`):]
	fresh = fresh[:strings.Index(fresh, "</section>")]
	if n := strings.Count(fresh, ".initech.example</div>"); n != 3 {
		t.Errorf("%d of Initech's targets listed, want 3", n)
	}
	for _, want := range []string{`<a class="name" href="/programs/fake/initech" title="Initech">Initech</a> <span class="chip private">private</span>`,
		`<span class="chip joined" title="New to you: in scope when you got access to this private program`, `>joined `,
		`<a class="more" href="/programs/fake/initech?tab=scope"`, `+2 more →</a>`, `<div class="prog-under"><div class="pn">`, "added or new to you in the last 14d"} {
		if !strings.Contains(fresh, want) {
			t.Errorf("fresh attack surface lacks %q", want)
		}
	}
	// The Targets page lists them all, marked the same way.
	_, body = e.do(c, http.MethodGet, "/targets?added=14d&sort=best&kind=domain", nil, nil)
	if n := strings.Count(body, `<span class="chip joined" title="New to you`); n != 5 {
		t.Errorf("Targets page marks %d targets joined, want Initech's 5", n)
	}
}
