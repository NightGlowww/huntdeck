package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Scope tab's "added in the last" chips narrow the rows, count what is
// in each window, and keep the choice in the form's hidden fields.
func TestScopeSinceWindow(t *testing.T) {
	now := time.Now()
	recent := now.Add(-2 * 24 * time.Hour)
	older := now.Add(-60 * 24 * time.Hour)
	d := &programData{InScope: []store.EntryView{
		{Identifier: "a.example.com", AssetType: "domain", AddedAt: &recent},
		{Identifier: "b.example.net", AssetType: "domain", AddedAt: &older},
		{Identifier: "c.example.org", AssetType: "domain"}, // undated
	}}

	all := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope", nil))
	if all.Total != 3 || len(all.SinceLinks) != 4 {
		t.Fatalf("all: %d rows, %d chips", all.Total, len(all.SinceLinks))
	}
	if !all.SinceLinks[0].On || all.SinceLinks[0].Count != 3 {
		t.Errorf("the 'All time' chip = %+v", all.SinceLinks[0])
	}

	week := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope&since=7d", nil))
	if week.Total != 1 || week.Rows[0].Identifier != "a.example.com" {
		t.Fatalf("7d: %d rows, first %q", week.Total, firstIdentifier(week.Rows))
	}
	if !week.SinceLinks[1].On || week.SinceLinks[1].Count != 1 {
		t.Errorf("the 7-day chip = %+v", week.SinceLinks[1])
	}
	if week.SinceLinks[2].Count != 1 || week.SinceLinks[3].Count != 2 {
		t.Errorf("30/90-day counts = %d, %d", week.SinceLinks[2].Count, week.SinceLinks[3].Count)
	}
	// Picking a window lands on newest first (the list had no order).
	if !strings.Contains(week.SinceLinks[1].URL, "order=added") {
		t.Errorf("the 7-day chip does not sort newest first: %s", week.SinceLinks[1].URL)
	}
	// An explicit order is kept.
	sorted := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope&since=30d&order=added", nil))
	if !strings.Contains(sorted.SinceLinks[2].URL, "order=added") || !sorted.SinceLinks[2].On {
		t.Errorf("30-day chip with an explicit order = %+v", sorted.SinceLinks[2])
	}

	// The search form keeps the window, and clearing filters drops it.
	if !hasParam(week.Hidden, "since", "7d") {
		t.Errorf("hidden fields lost the window: %+v", week.Hidden)
	}
	if got := week.ClearURL; got != "?tab=scope" {
		t.Errorf("ClearURL = %q", got)
	}

	// Removed entries are dated by their removal, so no window applies.
	removed := &programData{Removed: []store.EntryView{{Identifier: "gone.example.com", AssetType: "domain"}}}
	rv := removed.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope&show=removed&since=7d", nil))
	if rv.Total != 1 || len(rv.SinceLinks) != 0 {
		t.Errorf("removed view: %d rows, %d chips", rv.Total, len(rv.SinceLinks))
	}
}

func firstIdentifier(rows []store.EntryView) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Identifier
}

func hasParam(ps []param, name, value string) bool {
	for _, p := range ps {
		if p.Name == name && p.Value == value {
			return true
		}
	}
	return false
}

// The Scope tab shows the window chips in the page itself.
func TestScopeWindowChipsRender(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/programs/fake/acme?tab=scope", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"since=7d", "最近新增", "全部时间"} {
		if !strings.Contains(body, want) {
			t.Errorf("the scope tab lacks %q", want)
		}
	}
}

// The Scope tab's copy link answers with the rows the view shows, one
// identifier per line, and the page offers it as a button.
func TestScopeCopyText(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/programs/fake/acme?tab=scope&format=txt", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("copy text: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q", ct)
	}
	if strings.TrimSpace(body) == "" || strings.Contains(body, "<html") {
		t.Errorf("copy text body = %q", firstN(body, 120))
	}
	_, page := e.do(c, "GET", "/programs/fake/acme?tab=scope", nil, nil)
	if !strings.Contains(page, `data-copy-url="?`) || !strings.Contains(page, "format=txt") {
		t.Error("the scope tab has no copy button")
	}
	if !strings.Contains(page, `download="scope.txt"`) {
		t.Error("the scope tab has no download link")
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// The Disclosed page exports what its filters show: one report URL per
// line, behind copy and download buttons.
func TestDisclosedExportText(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/disclosed?format=txt", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q", ct)
	}
	if body != "" && !strings.HasPrefix(body, "http") {
		t.Errorf("export body starts %q", firstN(body, 60))
	}
	_, page := e.do(c, "GET", "/disclosed", nil, nil)
	for _, want := range []string{`data-copy-url="/disclosed?`, `download="disclosed.txt"`, "format=txt"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

// The Scope tab shows the first 1,000 rows; the copy and download links
// carry every row the filter matches (?all=1, which the export route sets).
func TestScopeExportCoversEveryRow(t *testing.T) {
	entries := make([]store.EntryView, scopeRowsShown+5)
	for i := range entries {
		entries[i] = store.EntryView{Identifier: fmt.Sprintf("h%d.example.com", i), AssetType: "domain"}
	}
	d := &programData{InScope: entries}

	page := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope", nil))
	if len(page.Rows) != scopeRowsShown || !page.Limited {
		t.Fatalf("page: %d rows, limited=%v", len(page.Rows), page.Limited)
	}

	export := d.scopeView(httptest.NewRequest("GET", "/programs/fake/acme?tab=scope&all=1", nil))
	if len(export.Rows) != len(entries) || export.Limited {
		t.Errorf("export: %d rows, limited=%v", len(export.Rows), export.Limited)
	}
}

// The Disclosed page can keep only reports that name a CVE; the export
// follows the same filter.
func TestDisclosedCVEFilter(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, page := e.do(c, "GET", "/disclosed?cve=1", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"仅带 CVE 的报告", "cve=1"} {
		if !strings.Contains(page, want) {
			t.Errorf("the filtered page lacks %q", want)
		}
	}
	res, body := e.do(c, "GET", "/disclosed?cve=1&format=txt", nil, nil)
	if res.StatusCode != http.StatusOK || (body != "" && !strings.HasPrefix(body, "http")) {
		t.Errorf("export with cve: %d %q", res.StatusCode, firstN(body, 60))
	}
}

// GET /api/v1/programs?format=csv hands back the list as the page's export
// does: a spreadsheet of every matching program, not a page of JSON.
func TestAPIProgramsCSV(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/api/v1/programs?format=csv", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("csv: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "platform,handle") {
		t.Fatalf("csv head = %q", firstN(body, 120))
	}
	// The JSON answer is unchanged.
	res, body = e.do(c, "GET", "/api/v1/programs", nil, nil)
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") || !strings.HasPrefix(body, "{") {
		t.Errorf("json answer = %q", firstN(body, 60))
	}
}

// The changes page offers a one-click "Scope changes" switch, and the
// switch keeps only scope kinds.
func TestChangesScopeChip(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, page := e.do(c, "GET", "/changes", nil, nil)
	if !strings.Contains(page, "group=scope") {
		t.Fatalf("the changes page has no scope switch")
	}
	res, scoped := e.do(c, "GET", "/changes?group=scope", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("scope view: %d", res.StatusCode)
	}
	if !strings.Contains(scoped, `class="qv on"`) {
		t.Errorf("the scope switch isn't marked as on")
	}
	// The chip carries the number it would show, before it is chosen.
	if !strings.Contains(page, "Scope changes <small>") {
		t.Errorf("the scope chip shows no count")
	}
}

// A Bark channel set up through the UI is saved, listed with its kind and a
// masked device key, and can be edited without losing its group.
func TestBarkChannelThroughTheUI(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	form := url.Values{"kind": {"bark"}, "name": {"Phone"}, "device_key": {"aBcD123456"},
		"group": {"Acme"}, "sound": {"alarm"}, "level": {"timeSensitive"},
		"instant": {"1"}, "event_invite": {"1"}}
	res, _ := e.do(c, http.MethodPost, "/settings/notifications", form, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	_, page := e.do(c, http.MethodGet, "/settings/notifications", nil, nil)
	for _, want := range []string{"Phone", "Bark", "device …3456"} {
		if !strings.Contains(page, want) {
			t.Errorf("the channel list lacks %q", want)
		}
	}
	// Editing shows the saved, non-secret settings back.
	id := regexp.MustCompile(`/settings/notifications/([0-9]+)`).FindStringSubmatch(page)
	if id == nil {
		t.Fatal("no channel to edit")
	}
	_, edit := e.do(c, http.MethodGet, "/settings/notifications/"+id[1], nil, nil)
	for _, want := range []string{`value="Acme"`, `value="alarm"`, `value="timeSensitive" selected`} {
		if !strings.Contains(edit, want) {
			t.Errorf("the edit form lacks %q", want)
		}
	}
}

// The Changes page keeps views too: its filters, sort and period under a
// name, saved in the browser like the other pages'.
func TestChangesViews(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, page := e.do(c, "GET", "/changes", nil, nil)
	if !strings.Contains(page, `data-views="/changes"`) {
		t.Fatalf("the changes page offers no saved views")
	}
	for _, key := range []string{"group", "scope", "change", "since", "bounty", "watch"} {
		if !strings.Contains(page, key) {
			t.Errorf("the views don't keep %q", key)
		}
	}
}

// The Changes page exports what its filters match: one change a line, every
// one of them, and the header offers copy and download.
func TestChangesExportText(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, "GET", "/changes?group=scope&format=txt", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q", ct)
	}
	if body != "" {
		first := strings.SplitN(strings.TrimSpace(body), "\n", 2)[0]
		if cols := strings.Split(first, "\t"); len(cols) != 4 {
			t.Errorf("a line has %d columns (want date, program, what, asset): %q", len(cols), first)
		}
	}
	_, page := e.do(c, "GET", "/changes", nil, nil)
	for _, want := range []string{`data-copy-url="/changes?`, `download="changes.txt"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

// The exports docs/api.md promises all answer, with the type it names: the
// page's filters, no paging, and JSON where a route offers both.
func TestDocumentedExportsAnswer(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	cases := []struct{ path, contentType string }{
		{"/programs/fake/acme?tab=scope&format=txt", "text/plain"},
		{"/api/v1/targets?kind=domain", "text/plain"},
		{"/api/v1/targets?kind=domain&format=json", "application/json"},
		{"/disclosed?format=txt", "text/plain"},
		{"/changes?format=txt", "text/plain"},
		{"/api/v1/programs?format=csv", "text/csv"},
	}
	for _, tc := range cases {
		res, body := e.do(c, "GET", tc.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", tc.path, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
			t.Errorf("%s: content type %q, want %q", tc.path, ct, tc.contentType)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("%s: answered a page, not an export", tc.path)
		}
	}
}
