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

	var hasNavGroupLinkStyle bool
	for _, r := range shellRules {
		if r.sel == ".nav .group" {
			if fsVal, ok := r.props["font-size"]; ok && !strings.Contains(fsVal, "var(--fs,") {
				t.Errorf("02-shell.css: .nav .group hardcodes font-size: %q without var(--fs,", fsVal)
			}
		}
		if r.sel == ".nav a.nav-group-link" && r.media == "" {
			if r.props["justify-content"] == "space-between" && strings.Contains(r.props["font-size"], "var(--fs,") {
				hasNavGroupLinkStyle = true
			}
		}
	}
	if !hasNavGroupLinkStyle {
		t.Error("02-shell.css: missing .nav a.nav-group-link section heading style")
	}

	respCSS, err := fs.ReadFile(staticFS, "static/css/10-responsive.css")
	if err != nil {
		t.Fatal(err)
	}
	respRules := parseCSS(string(respCSS), "10-responsive.css")

	var (
		hasCollapsed1100, hasCollapsed1100Label, hasCollapsed1100Chev         bool
		hasCollapsedMin, hasCollapsedMinLabel, hasCollapsedMinChev            bool
		hasMobileHidden, hasMobileOpen, hasMobileOpenLabel, hasMobileOpenChev bool
	)
	for _, r := range respRules {
		switch {
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav a.nav-group-link":
			if r.props["height"] == "1px" && r.props["width"] == "24px" {
				hasCollapsed1100 = true
			}
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav a.nav-group-link .group-label":
			if r.props["position"] == "absolute" && strings.Contains(r.props["clip"], "rect(") {
				hasCollapsed1100Label = true
			}
		case strings.Contains(r.media, "max-width: 1100px") && r.sel == ".nav a.nav-group-link .group-chev":
			if r.props["display"] == "none" {
				hasCollapsed1100Chev = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav a.nav-group-link`:
			if r.props["height"] == "1px" && r.props["width"] == "24px" {
				hasCollapsedMin = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav a.nav-group-link .group-label`:
			if r.props["position"] == "absolute" && strings.Contains(r.props["clip"], "rect(") {
				hasCollapsedMinLabel = true
			}
		case strings.Contains(r.media, "min-width: 1101px") && r.sel == `:root[data-side="min"] .nav a.nav-group-link .group-chev`:
			if r.props["display"] == "none" {
				hasCollapsedMinChev = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".nav a.nav-group-link":
			if r.props["display"] == "none" {
				hasMobileHidden = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav a.nav-group-link":
			if r.props["display"] == "flex" && r.props["justify-content"] == "space-between" && r.props["width"] == "auto" {
				hasMobileOpen = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav a.nav-group-link .group-label":
			if r.props["position"] == "static" && r.props["clip"] == "auto" {
				hasMobileOpenLabel = true
			}
		case strings.Contains(r.media, "max-width: 700px") && r.sel == ".js .sidebar.nav-open .nav a.nav-group-link .group-chev":
			if r.props["display"] == "block" {
				hasMobileOpenChev = true
			}
		}
	}
	for _, check := range []struct {
		ok   bool
		desc string
	}{
		{hasCollapsed1100, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav a.nav-group-link divider rule"},
		{hasCollapsed1100Label, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav a.nav-group-link .group-label clip rule"},
		{hasCollapsed1100Chev, "10-responsive.css: missing collapsed @media (max-width: 1100px) .nav a.nav-group-link .group-chev hide rule"},
		{hasCollapsedMin, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav a.nav-group-link divider rule`},
		{hasCollapsedMinLabel, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav a.nav-group-link .group-label clip rule`},
		{hasCollapsedMinChev, `10-responsive.css: missing collapsed @media (min-width: 1101px) :root[data-side="min"] .nav a.nav-group-link .group-chev hide rule`},
		{hasMobileHidden, "10-responsive.css: missing @media (max-width: 700px) .nav a.nav-group-link { display: none } rule"},
		{hasMobileOpen, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav a.nav-group-link restore rule"},
		{hasMobileOpenLabel, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav a.nav-group-link .group-label restore rule"},
		{hasMobileOpenChev, "10-responsive.css: missing @media (max-width: 700px) .js .sidebar.nav-open .nav a.nav-group-link .group-chev restore rule"},
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
	for _, want := range []string{
		`<a class="nav-group-link" href="/system" title="System"><span class="group-label">System</span><svg class="i group-chev" aria-hidden="true"><use href="#i-chev"/></svg></a>`,
		`<a class="nav-group-link" href="/integrations" title="Integrations"><span class="group-label">Integrations</span><svg class="i group-chev" aria-hidden="true"><use href="#i-chev"/></svg></a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /: missing structured nav-group-link markup %s", want)
		}
	}
}
