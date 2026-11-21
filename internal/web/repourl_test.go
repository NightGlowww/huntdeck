package web

import (
	"net/http"
	"strings"
	"testing"
)

// HuntDeck Public links to the source repository, for visitors to run their
// own copy: in the banner (its one call to action), the sidebar, the notes on the pages it shows
// empty, the feedback page, llms.txt and the site's structured data. A full
// install never does.
func TestRepoURL(t *testing.T) {
	const repo = "https://github.com/example/huntdeck"
	e := newTestEnv(t, func(d *Deps) {
		d.ReadOnly, d.RepoURL, d.FeedbackEmail, d.PublicURL = true, repo, "hello@example.com", "https://demo.example.com"
	})
	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{`<a class="db-cta" href="` + repo + `"`, "Self-host free", "Get HuntDeck", `"sameAs":["` + repo + `"]`}},
		{"/settings", []string{`<b>Self-hosted</b>`, `Self-host HuntDeck</a></span></p>`}},
		{"/feedback", []string{`href="` + repo + `/issues"`}},
		{"/programs", []string{`<a href="` + repo + `/issues/new" target="_blank"`, "Open an issue on GitHub"}},
		{"/.well-known/security.txt", []string{"Contact: " + repo + "/security/advisories/new\n"}},
		{"/llms.txt", []string{"[Self-host HuntDeck](" + repo + ")", "This site is HuntDeck Public"}},
	}
	for _, tt := range tests {
		_, body := e.do(e.client(), http.MethodGet, tt.path, nil, nil)
		for _, want := range tt.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", tt.path, want)
			}
		}
	}
	// The banner is the sentence and the repository: feedback and the X
	// account are in the sidebar.
	if _, body := e.do(e.client(), http.MethodGet, "/", nil, nil); strings.Contains(body, "Send feedback") || strings.Contains(body, "db-fb") {
		t.Error("the banner still has a feedback link")
	}
	full := newTestEnv(t, func(d *Deps) { d.RepoURL = repo })
	if _, body := full.do(full.client(), http.MethodGet, "/", nil, nil); strings.Contains(body, repo) {
		t.Error("a full install links to the source repository")
	}
}
