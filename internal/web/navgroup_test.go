package web

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestNavGroupStylesAndMarkup(t *testing.T) {
	shellCSS, err := fs.ReadFile(staticFS, "static/css/02-shell.css")
	if err != nil {
		t.Fatal(err)
	}
	shellRules := parseCSS(string(shellCSS), "02-shell.css")

	var hasNavGroupLinkStyle, hasNavGroupHeadStyle, hasToggleHoverTransparent bool
	for _, r := range shellRules {
		if r.sel == ".nav .group" {
			if fsVal, ok := r.props["font-size"]; ok && !strings.Contains(fsVal, "var(--fs,") {
				t.Errorf("02-shell.css: .nav .group hardcodes font-size: %q without var(--fs,", fsVal)
			}
		}
		if r.sel == ".nav a.nav-group-link" && r.media == "" {
			if strings.Contains(r.props["font-size"], "var(--fs,") {
				hasNavGroupLinkStyle = true
			}
		}
		if r.sel == ".nav-group-head" && r.media == "" {
			if r.props["display"] == "flex" {
				hasNavGroupHeadStyle = true
			}
		}
		if strings.Contains(r.sel, "button.nav-group-toggle:hover") && r.media == "" {
			if r.props["background"] == "transparent" {
				hasToggleHoverTransparent = true
			}
		}
	}
	if !hasNavGroupLinkStyle {
		t.Error("02-shell.css: missing .nav a.nav-group-link section heading style")
	}
	if !hasNavGroupHeadStyle {
		t.Error("02-shell.css: missing .nav-group-head flex row style")
	}
	if !hasToggleHoverTransparent {
		t.Error("02-shell.css: button.nav-group-toggle:hover must set background: transparent to override button:hover in 09-forms.css")
	}

	respCSS, err := fs.ReadFile(staticFS, "static/css/10-responsive.css")
	if err != nil {
		t.Fatal(err)
	}
	respRules := parseCSS(string(respCSS), "10-responsive.css")

	var (
		hasCollapsed1100, hasCollapsed1100Label, hasCollapsed1100Toggle         bool
		hasCollapsedMin, hasCollapsedMinLabel, hasCollapsedMinToggle            bool
		hasMobileHidden, hasMobileOpen, hasMobileOpenLabel, hasMobileOpenToggle bool
	)
	for _, r := range respRules {
		switch {
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav a.nav-group-link":
			if r.props["width"] == "42px" && r.props["justify-content"] == "center" {
				hasCollapsed1100 = true
			}
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav a.nav-group-link .group-label":
			if r.props["position"] == "absolute" && strings.Contains(r.props["clip"], "rect(") {
				hasCollapsed1100Label = true
			}
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav-group-toggle":
			if r.props["display"] == "none" {
				hasCollapsed1100Toggle = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav a.nav-group-link`:
			if r.props["width"] == "42px" && r.props["justify-content"] == "center" {
				hasCollapsedMin = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav a.nav-group-link .group-label`:
			if r.props["position"] == "absolute" && strings.Contains(r.props["clip"], "rect(") {
				hasCollapsedMinLabel = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav-group-toggle`:
			if r.props["display"] == "none" {
				hasCollapsedMinToggle = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".nav a.nav-group-link":
			if r.props["display"] == "none" {
				hasMobileHidden = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav a.nav-group-link":
			if r.props["display"] == "flex" && r.props["justify-content"] == "flex-start" && r.props["width"] == "auto" {
				hasMobileOpen = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav a.nav-group-link .group-label":
			if r.props["position"] == "static" && r.props["clip"] == "auto" {
				hasMobileOpenLabel = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav-group-toggle":
			if r.props["display"] == "inline-grid" {
				hasMobileOpenToggle = true
			}
		}
	}
	for _, check := range []struct {
		ok   bool
		desc string
	}{
		{hasCollapsed1100, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav a.nav-group-link icon rule"},
		{hasCollapsed1100Label, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav a.nav-group-link .group-label clip rule"},
		{hasCollapsed1100Toggle, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav-group-toggle hide rule"},
		{hasCollapsedMin, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav a.nav-group-link icon rule`},
		{hasCollapsedMinLabel, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav a.nav-group-link .group-label clip rule`},
		{hasCollapsedMinToggle, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav-group-toggle hide rule`},
		{hasMobileHidden, "10-responsive.css: missing @media (max-width: 700px) .nav a.nav-group-link { display: none } rule"},
		{hasMobileOpen, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav a.nav-group-link restore rule"},
		{hasMobileOpenLabel, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav a.nav-group-link .group-label restore rule"},
		{hasMobileOpenToggle, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav-group-toggle restore rule"},
	} {
		if !check.ok {
			t.Error(check.desc)
		}
	}

	e := newTestEnv(t)
	res, body := e.do(e.client(), http.MethodGet, "/", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status %d", res.StatusCode)
	}
	if strings.Contains(body, "group-chev") {
		t.Error("GET /: double chevron (.group-chev) must not be rendered inside nav-group-link")
	}
	for _, want := range []string{
		`<a class="nav-group-link" href="/system" title="System"><svg class="i" aria-hidden="true"><use href="#i-cog"/></svg><span class="group-label">System</span></a>`,
		`<a class="nav-group-link" href="/integrations" title="Integrations"><svg class="i" aria-hidden="true"><use href="#i-layers"/></svg><span class="group-label">Integrations</span></a>`,
		`data-nav-toggle-section="system"`,
		`data-nav-toggle-section="integrations"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /: missing structured nav-group-link markup %s", want)
		}
	}
}

// strict CSP enforces style-src 'self' (no 'unsafe-inline'), so any inline
// style="" attribute in a template is silently dropped by browsers.
func TestNoInlineStylesInTemplates(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return walkErr
		}
		b, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `style="`) || strings.Contains(string(b), `style='`) {
			t.Errorf("%s contains inline style attribute (blocked by style-src 'self' CSP)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
