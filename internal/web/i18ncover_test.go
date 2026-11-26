package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// Every string a template asks the catalog for exists in it. Without this,
// a missing entry is invisible: T() falls back to the English source, so the
// page simply shows English and no test notices.
func TestTemplatesAreTranslated(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	pat := regexp.MustCompile(`\{\{-?\s*TF?\s+"([^"]*)"`)
	var missing []string
	seen := 0
	err = fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		body, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}
		for _, m := range pat.FindAllStringSubmatch(string(body), -1) {
			seen++
			if !b.HasMsg("zh", m[1]) {
				missing = append(missing, path+": "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d template string(s) have no zh translation:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	// A regex that stopped matching would leave missing empty and pass: the
	// count is what says the guard is still looking at the templates.
	if seen < 400 {
		t.Fatalf("only %d T/TF calls found across the templates: the pattern stopped matching", seen)
	}
	t.Logf("checked %d template strings", seen)
}

// TestNoTemplateSyntaxInsideT guards against Go template syntax ({{...}})
// embedded inside {{T "..."}} or {{TF "..."}} message strings or catalog
// entries. T() looks up the literal string without executing template actions,
// so any {{...}} inside a translation key prints raw syntax to the user.
func TestNoTemplateSyntaxInsideT(t *testing.T) {
	tCallPat := regexp.MustCompile(`^\{\{-?\s*TF?\s+"((?:[^"\\]|\\.)*)"`)
	var bad []string
	seenActions := 0
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		body, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}
		for _, seg := range splitKeepingActions(string(body)) {
			if !seg.isAction {
				continue
			}
			if m := tCallPat.FindStringSubmatch(seg.text); m != nil {
				seenActions++
				if strings.Contains(m[1], "{{") || strings.Contains(m[1], "}}") {
					bad = append(bad, path+": "+m[1])
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenActions < 400 {
		t.Fatalf("only %d T/TF calls scanned: pattern stopped matching", seenActions)
	}

	catalogs, err := filepath.Glob(filepath.Join("..", "i18n", "catalog", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs) == 0 {
		t.Fatal("no i18n catalog files found")
	}
	for _, catPath := range catalogs {
		raw, err := os.ReadFile(catPath)
		if err != nil {
			t.Fatal(err)
		}
		var cat struct {
			Messages map[string]string `json:"messages"`
		}
		if err := json.Unmarshal(raw, &cat); err != nil {
			t.Fatalf("%s: %v", catPath, err)
		}
		for k, v := range cat.Messages {
			if strings.Contains(k, "{{") || strings.Contains(k, "}}") || strings.Contains(v, "{{") || strings.Contains(v, "}}") {
				bad = append(bad, catPath+": "+k)
			}
		}
	}

	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("%d template or catalog entry(ies) contain {{ or }}:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// TestReportsAndAnalyticsRenderCleanChinese verifies that key Chinese pages
// render without raw Go template braces ({{ or }}), without outdated
// machine/court translations ("三审", "维持原判"), and with expected Chinese copy.
func TestReportsAndAnalyticsRenderCleanChinese(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Mail = newFakeMail() })
	e.addReports(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	for _, p := range []string{"/", "/queue", "/disclosed", "/email", "/email/stats", "/email/m1", "/system", "/integrations", "/reports", "/reports/analytics"} {
		res, body := e.do(e.client(), "GET", p, nil, h)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", p, res.StatusCode)
			continue
		}
		if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
			t.Errorf("%s rendered raw template braces ({{ or }})", p)
		}
		for _, bad := range []string{"三审", "维持原判"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s rendered deprecated translation %q", p, bad)
			}
		}
	}
	_, disc := e.do(e.client(), "GET", "/disclosed", nil, h)
	if !strings.Contains(disc, "披露于") {
		t.Error("/disclosed missing Chinese disclosure tooltip")
	}
	_, email := e.do(e.client(), "GET", "/email", nil, h)
	if !strings.Contains(email, "条消息") && !strings.Contains(email, "收件人") {
		t.Error("/email missing Chinese mail UI copy")
	}
	_, rep := e.do(e.client(), "GET", "/reports", nil, h)
	for _, want := range []string{"待你回复", "久无回应"} {
		if !strings.Contains(rep, want) {
			t.Errorf("/reports missing %q", want)
		}
	}
	_, ana := e.do(e.client(), "GET", "/reports/analytics", nil, h)
	for _, want := range []string{"分诊", "报告分析", "与提交一致"} {
		if !strings.Contains(ana, want) {
			t.Errorf("/reports/analytics missing %q", want)
		}
	}
}
