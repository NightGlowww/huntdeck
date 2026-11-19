package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// The Invites page and API list the private programs, pending invitations
// first, each with an Accept button; the hunt board's Invites box lists the
// pending ones with theirs.
func TestInvites(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	sent := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if _, _, err := e.store.SetInvites(ctx, "fake", []platform.Invite{{Handle: "evil", InvitedAt: sent}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	res, body := e.do(c, http.MethodGet, "/invites", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/invites = %d", res.StatusCode)
	}
	for _, want := range []string{
		`<a href="/invites" aria-current="page">`, "1 open invite</span>", "&lt;script&gt;", `<span class="chip waiting" title="Invited 2026-10-06`,
		`<span class="fl">Not accepted yet</span><em>1</em>`, `<form class="fsearch" method="get" action="/invites">`,
		`<table class="fixed plist-table cards has-accept"`, `<form class="accept-form" method="post" action="/invites/fake/evil/accept" data-busy="Accepting…" data-stay-busy data-confirm="Accept the invitation to &lt;script&gt;`,
		`<input type="hidden" name="back" value="/invites">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/invites lacks %q", want)
		}
	}
	for _, not := range []string{"Acme Corp", ">Visibility<", `<span class="chip private">`} {
		if strings.Contains(body, not) {
			t.Errorf("/invites has %q", not)
		}
	}
	if _, body := e.do(c, http.MethodGet, "/invites?invite=joined", nil, nil); !strings.Contains(body, "No invites match these filters") {
		t.Error("/invites?invite=joined should be empty")
	}
	// Programs marks it too, coming back to the same list.
	if _, body := e.do(c, http.MethodGet, "/programs?sort=name&msg=poll-started", nil, nil); !strings.Contains(body, `not accepted yet">pending`) ||
		!strings.Contains(body, `action="/invites/fake/evil/accept"`) || !strings.Contains(body, `name="back" value="/programs?sort=name">`) {
		t.Error("/programs doesn't offer to accept the pending invitation")
	}

	var out struct {
		Total    int `json:"total"`
		Programs []struct {
			Handle string `json:"handle"`
			Invite *struct {
				Pending   bool       `json:"pending"`
				InvitedAt *time.Time `json:"invited_at"`
			} `json:"invite"`
		} `json:"programs"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/invites", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 1 || out.Programs[0].Handle != "evil" ||
		out.Programs[0].Invite == nil || !out.Programs[0].Invite.Pending || !out.Programs[0].Invite.InvitedAt.Equal(sent) {
		t.Errorf("/api/v1/invites = %s", body)
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/programs?sort=name", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 2 {
		t.Fatalf("/api/v1/programs = %s", body)
	}
	for _, p := range out.Programs {
		if (p.Invite != nil) != (p.Handle == "evil") {
			t.Errorf("%s: invite %+v (only private programs have one)", p.Handle, p.Invite)
		}
	}

	var one struct {
		Invite *struct {
			Pending bool `json:"pending"`
		} `json:"invite"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/evil", nil, nil)
	if err := json.Unmarshal([]byte(body), &one); err != nil || one.Invite == nil || !one.Invite.Pending {
		t.Errorf("/api/v1/programs/fake/evil invite = %s", body[:min(len(body), 300)])
	}
	// The API page's examples show it.
	_, body = e.do(c, http.MethodGet, "/api", nil, nil)
	if i := strings.Index(body, `id="listInvites"`); i < 0 || !strings.Contains(body[i:i+min(len(body)-i, 9000)], "&#34;invite&#34;: {") {
		t.Error("the API page's invites example lacks the invite")
	}

	_, body = e.do(c, http.MethodGet, "/", nil, nil)
	i := strings.Index(body, "<h2>Invites</h2>")
	if i < 0 {
		t.Fatal("the hunt board has no Invites box")
	}
	for _, want := range []string{"Waiting for you · 1", `<div class="inv-act"><form class="accept-form" method="post" action="/invites/fake/evil/accept"`,
		`name="back" value="/">`, "Invited ", `<a class="more-link" href="/invites">`} {
		if !strings.Contains(body[i:], want) {
			t.Errorf("the Invites box lacks %q", want)
		}
	}
}

// Accept buttons accept through the Poller and come back to the page with
// how it went; the API does the same.
func TestAcceptInvite(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, tc := range []struct {
		back, wantLoc string
		err           error
		apiStatus     int
	}{
		{"/invites", "/invites?msg=invite-accepted", nil, http.StatusOK},
		{"/programs?sort=name", "/programs?sort=name&msg=invite-accepted", nil, http.StatusOK},
		{"/", "/?msg=invite-accepting", ErrStillAccepting, http.StatusAccepted},
		{"//elsewhere.example/x", "/invites?err=invite-none", ErrNoInvite, http.StatusNotFound},
		{"https://elsewhere.example/", "/invites?err=invite-cannot", ErrCannotAccept, http.StatusNotFound},
		{"", "/invites?err=invite-off", ErrPlatformDisabled, http.StatusConflict},
		{"/invites", "/invites?err=invite-failed", errors.New("intigriti: accept invite: HTTP 500"), http.StatusBadGateway},
	} {
		e.poller.acceptErr = tc.err
		res, _ := e.do(c, http.MethodPost, "/invites/fake/evil/accept", url.Values{"back": {tc.back}}, nil)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != tc.wantLoc {
			t.Errorf("back %q, %v: %d to %q, want %q", tc.back, tc.err, res.StatusCode, res.Header.Get("Location"), tc.wantLoc)
		}
		if res, body := e.do(c, http.MethodPost, "/api/v1/invites/fake/evil/accept", nil, nil); res.StatusCode != tc.apiStatus {
			t.Errorf("API, %v: %d %s, want %d", tc.err, res.StatusCode, body, tc.apiStatus)
		}
	}
	if n := len(e.poller.accepted); n != 14 || e.poller.accepted[0] != "fake/evil" {
		t.Errorf("accepted %v", e.poller.accepted)
	}
	// Only by POST, and not from another site.
	if res, _ := e.do(c, http.MethodGet, "/invites/fake/evil/accept", nil, nil); res.StatusCode == http.StatusSeeOther {
		t.Error("GET accepts")
	}
	res, _ := e.do(c, http.MethodPost, "/invites/fake/evil/accept", url.Values{}, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if res.StatusCode != http.StatusForbidden || len(e.poller.accepted) != 14 {
		t.Errorf("cross-site POST: %d, accepted %d", res.StatusCode, len(e.poller.accepted))
	}
	// The flash messages read.
	_, body := e.do(c, http.MethodGet, "/invites?msg=invite-accepted", nil, nil)
	if !strings.Contains(body, "Invitation accepted. HuntDeck is refreshing that program now") {
		t.Error("/invites doesn't say it was accepted")
	}
}

func TestLocalPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/invites", "/invites"},
		{"/programs?sort=name&q=a+b", "/programs?sort=name&q=a+b"},
		{"/", "/"},
		{"", "/d"},
		{"invites", "/d"},
		{"//evil.example/x", "/d"},
		{"/%2F%2Fevil.example", "/d"},
		{"/\\evil.example", "/d"},
		{"https://evil.example/", "/d"},
		{"javascript:alert(1)", "/d"},
		{"/a\r\nSet-Cookie: x=1", "/d"},
	} {
		if got := localPath(tc.in, "/d"); got != tc.want {
			t.Errorf("localPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
