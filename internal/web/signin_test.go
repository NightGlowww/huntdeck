package web

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestSignInLines(t *testing.T) {
	ago := func(d time.Duration) *time.Time { v := time.Now().Add(-d); return &v }
	runs := []store.Run{{Platform: "p", Mode: "quick", Status: store.RunOK, StartedAt: time.Now().Add(-time.Hour)},
		{Platform: "p", Mode: "full", Status: store.RunFailed, StartedAt: time.Now()}}
	tests := []struct {
		name  string
		creds []platform.Credential
		web   *platform.WebSession
		runs  []store.Run
		want  []string // "state label name: parts | problem"
	}{
		{"accepted", []platform.Credential{{Name: "API token", LastOK: ago(time.Minute)}}, nil, nil,
			[]string{"ok ok API token: accepted"}},
		{"a login's session", []platform.Credential{{Name: "Login", LastOK: ago(time.Minute), SignedIn: ago(3 * time.Hour), Expires: ago(-time.Hour)}}, nil, nil,
			[]string{"ok ok Login: accepted, signed in, expires"}},
		{"refused since", []platform.Credential{{Name: "Login", LastOK: ago(time.Hour), LastFail: ago(time.Minute), Failure: "password not accepted", Stuck: true}}, nil, nil,
			[]string{"failed rejected Login: refused, not retried until a restart | password not accepted"}},
		{"vouched for by the last good poll", []platform.Credential{{Name: "API token"}}, nil, runs,
			[]string{"ok ok API token: accepted at the last poll"}},
		{"not used yet", []platform.Credential{{Name: "API token"}}, nil, nil,
			[]string{" not checked API token: checked at the next poll"}},
		{"public only", []platform.Credential{}, nil, nil, []string{" public No login: public programs only"}},
		{"website signed in", []platform.Credential{{Name: "API token", LastOK: ago(time.Minute)}},
			&platform.WebSession{SignedIn: true, LastRenewal: ago(time.Hour)}, nil,
			[]string{"ok ok API token: accepted", "ok ok Website login: signed in, renewed"}},
		{"website rejected", nil, &platform.WebSession{Stuck: true, Failure: "bad password.", LastFailure: ago(time.Hour)}, nil,
			[]string{"failed rejected Website login: refused | bad password. Fix it in .env and restart."}},
	}
	s := &server{}
	for _, tt := range tests {
		p := PlatformInfo{Name: "p"}
		if tt.creds != nil {
			p.Credentials = func() []platform.Credential { return tt.creds }
		}
		var web *webSyncRow
		if tt.web != nil {
			web = &webSyncRow{Session: *tt.web}
		}
		var got []string
		for _, l := range s.signIns(p, tt.runs, web) {
			var parts []string
			for _, pt := range l.Parts {
				parts = append(parts, pt.TextKey)
			}
			line := l.State + " " + l.LabelKey + " " + l.NameKey + ": " + strings.Join(parts, ", ")
			if l.Problem != "" {
				line += " | " + l.Problem
			}
			got = append(got, line)
		}
		if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}
