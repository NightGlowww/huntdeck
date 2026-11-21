package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
)

// The Programs list narrows to several platforms at once; each turns
// itself on and off, and a view keeps them.
func TestProgramsSeveralPlatforms(t *testing.T) {
	e := newTestEnv(t)
	if err := e.syncer.SaveOwn(context.Background(), []independent.Program{{Program: platform.Program{Handle: "initech", Name: "Initech",
		URL: "https://initech.example/", State: platform.StateOpen, OffersBounty: true}}}); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	for _, tt := range []struct {
		path string
		n    int
		want []string
	}{
		{"/programs?platform=fake", 2, []string{
			// Fake turns off; Independent joins it.
			`<a class="fc on" href="/programs" aria-current="true">`,
			`<a class="fc" href="/programs?platform=fake%2Cindependent">`}},
		{"/programs?platform=independent,fake", 3, []string{
			`<a class="fc on" href="/programs?platform=independent" aria-current="true">`,
			`<a class="fc on" href="/programs?platform=fake" aria-current="true">`,
			// A view keeps both.
			`href="/programs?bounty=1&amp;platform=fake%2Cindependent&amp;sort=huntscore"`}},
		{"/programs?bounty=1&sort=huntscore&platform=fake,independent", 2, []string{
			`<a class="fc on" href="/programs?platform=fake%2Cindependent" title="Highest HuntScore among paying programs" aria-current="page">`}},
		{"/programs?platform=h1", 0, nil}, // a short name of a platform with no programs here
	} {
		res, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		if res.StatusCode != http.StatusOK || strings.Count(body, `<tr class="prow"`) != tt.n {
			t.Errorf("%s = %d, %d rows, want %d", tt.path, res.StatusCode, strings.Count(body, `<tr class="prow"`), tt.n)
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %s", tt.path, w)
			}
		}
	}
	var out struct {
		Programs []struct{ Platform string } `json:"programs"`
	}
	_, body := e.do(c, http.MethodGet, "/api/v1/programs?platform=fake,independent&visibility=public", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range out.Programs {
		got = append(got, p.Platform)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"fake", "independent"}) {
		t.Errorf("API platforms = %q", got)
	}
}

// /programs/<platform>, an address people guess from a program's, goes to
// the Programs list narrowed to that platform.
func TestProgramsOfPlatform(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, tt := range []struct {
		path, want string
		code       int
	}{
		{"/programs/fake", "/programs?platform=fake", http.StatusMovedPermanently},
		{"/programs/fake/", "/programs?platform=fake", http.StatusMovedPermanently},
		{"/programs/FAKE/", "/programs?platform=fake", http.StatusMovedPermanently},
		{"/programs/nope/", "", http.StatusNotFound},
		{"/programs/hackerone/", "", http.StatusNotFound}, // not configured here
		{"/programs/fake/acme", "", http.StatusOK},        // a program is still a program
	} {
		res, _ := e.do(c, http.MethodGet, tt.path, nil, nil)
		if res.StatusCode != tt.code || res.Header.Get("Location") != tt.want {
			t.Errorf("%s: %d %q, want %d %q", tt.path, res.StatusCode, res.Header.Get("Location"), tt.code, tt.want)
		}
	}
}
