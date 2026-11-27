package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/store"
)

// fakeRestarter records restarts; while pending it waits for waiting.
type fakeRestarter struct {
	asked   []bool // each Restart's now
	waiting []store.Run
}

func (f *fakeRestarter) Restart(now bool) { f.asked = append(f.asked, now) }

func (f *fakeRestarter) Pending(context.Context) (bool, []store.Run) {
	return len(f.asked) > 0, f.waiting
}

const (
	envToken = "env-token-0b7c1e"
	newToken = "saved-token-9d4f2a"
)

func newAccountsEnv(t *testing.T) (*testEnv, *overrides.Saved, *fakeRestarter) {
	t.Helper()
	env := map[string]string{"IT_TOKEN": envToken, "BC_PUBLIC_ONLY": "true"}
	src := config.Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}
	var saved *overrides.Saved
	rs := &fakeRestarter{}
	e := newTestEnv(t, func(d *Deps) {
		saved = overrides.New(d.Store, t.TempDir(), src)
		d.Accounts, d.Restart = saved, rs
	})
	return e, saved, rs
}

func TestAccountsTab(t *testing.T) {
	ctx := context.Background()
	plain := newTestEnv(t)
	if _, body := plain.do(plain.client(), "GET", "/settings?tab=accounts", nil, nil); strings.Contains(body, `href="/settings?tab=accounts"`) {
		t.Error("the Accounts tab shows where settings can't be saved")
	}

	e, saved, rs := newAccountsEnv(t)
	c := e.client()
	_, body := e.do(c, "GET", "/settings?tab=accounts", nil, nil)
	for _, want := range []string{`href="/settings?tab=accounts"`, `id="acct-hackerone"`, `id="acct-bugcrowd"`, `id="acct-vulbox"`, `id="acct-issuehunt"`, `id="acct-gobugfree"`, `id="acct-inspectiv"`, `id="acct-network"`,
		`name="IT_TOKEN"`, `name="VB_ACCOUNT"`, `name="VB_SESSION"`, `name="VB_AUTO_JOIN" value="true" checked`, `name="IH_API_KEY"`, `name="IH_PUBLIC_ONLY"`, `name="GBF_EMAIL"`, `name="GBF_TOKEN"`, `name="GBF_PUBLIC_ONLY"`, `name="INS_EMAIL"`, `name="INS_TOKEN"`, `name="BC_PUBLIC_ONLY" value="true" checked`, `placeholder="set · type to replace"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Accounts tab lacks %s", want)
		}
	}
	if strings.Contains(body, envToken) {
		t.Fatal("the page shows a secret from the environment")
	}

	// Saving a token: encrypted, never shown, and a restart is asked for;
	// the platform is polled after it.
	res, _ := e.do(c, "POST", "/settings/accounts", url.Values{"group": {"intigriti"}, "IT_TOKEN": {newToken}}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "msg=accounts-saved") {
		t.Fatalf("save: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if len(rs.asked) != 1 || rs.asked[0] {
		t.Errorf("restarts asked: %v, want one, once polls finish", rs.asked)
	}
	cfg, err := saved.Config(ctx)
	if err != nil || cfg.Intigriti.Token.Reveal() != newToken {
		t.Fatalf("token not saved: %v", err)
	}
	if names, _ := saved.TakePollAfterRestart(ctx); len(names) != 1 || names[0] != "intigriti" {
		t.Errorf("polled after the restart: %v", names)
	}
	_, body = e.do(c, "GET", "/settings?tab=accounts", nil, nil)
	if strings.Contains(body, newToken) || !strings.Contains(body, "saved here") || !strings.Contains(body, "data-restart") {
		t.Error("after saving: the token shows, or it isn't marked saved, or the restart isn't shown")
	}

	// An untouched form changes nothing: empty secrets keep theirs, and
	// the switch matching .env's isn't saved over it.
	res, _ = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"bugcrowd"}, "BC_PUBLIC_ONLY": {"true"}, "BC_RATE": {""}}, nil)
	if !strings.Contains(res.Header.Get("Location"), "msg=accounts-unchanged") {
		t.Errorf("untouched form: %s", res.Header.Get("Location"))
	}

	// Unchecking a default-true switch submits empty via its hidden sibling and saves false.
	res, _ = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"vulbox"}, "VB_AUTO_JOIN": {""}}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "msg=accounts-saved") {
		t.Fatalf("uncheck VB_AUTO_JOIN: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if cfg, err := saved.Config(ctx); err != nil || cfg.VulBox.AutoJoin {
		t.Fatalf("VB_AUTO_JOIN after uncheck: %v (err=%v), want false", cfg.VulBox.AutoJoin, err)
	}

	// An invalid one is refused, saying why without the value.
	res, body = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"bugcrowd"}, "BC_PUBLIC_ONLY": {"true"}, "BC_EMAIL": {"me@example.com"}}, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "BC_EMAIL and BC_PASSWORD") {
		t.Errorf("invalid save: %d", res.StatusCode)
	}
	res, body = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"gobugfree"}, "GBF_PUBLIC_ONLY": {"true"}, "GBF_EMAIL": {"me@example.com"}}, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "GBF_EMAIL and GBF_PASSWORD") {
		t.Errorf("invalid gobugfree save: %d", res.StatusCode)
	}
	res, _ = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"issuehunt"}, "IH_API_KEY": {newToken}}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "msg=accounts-saved") {
		t.Fatalf("save IH_API_KEY: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if cfg, err := saved.Config(ctx); err != nil || cfg.IssueHunt.APIKey.Reveal() != newToken {
		t.Fatalf("IH_API_KEY not saved: %v", err)
	}
	res, body = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"inspectiv"}, "INS_EMAIL": {"me@example.com"}}, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "INS_EMAIL and INS_PASSWORD") {
		t.Errorf("invalid inspectiv save: %d", res.StatusCode)
	}

	// Clearing, then resetting back to .env.
	e.do(c, "POST", "/settings/accounts", url.Values{"group": {"intigriti"}, "clear_IT_TOKEN": {"1"}}, nil)
	if cfg, _ := saved.Config(ctx); cfg.Intigriti.Enabled() {
		t.Error("cleared token still counts")
	}
	e.do(c, "POST", "/settings/accounts/reset", url.Values{"group": {"intigriti"}}, nil)
	if cfg, _ := saved.Config(ctx); cfg.Intigriti.Token.Reveal() != envToken {
		t.Error("reset didn't go back to the environment's token")
	}

	// Unknown groups, and fields from another group, are ignored or refused.
	if res, _ := e.do(c, "POST", "/settings/accounts", url.Values{"group": {"server"}, "HUNTDECK_LISTEN": {":1"}}, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown group: %d", res.StatusCode)
	}
	res, _ = e.do(c, "POST", "/settings/accounts", url.Values{"group": {"yeswehack"}, "IT_TOKEN": {"x"}}, nil)
	if !strings.Contains(res.Header.Get("Location"), "accounts-unchanged") {
		t.Errorf("another group's field was saved: %s", res.Header.Get("Location"))
	}

	// Restart now, and the status the page's script asks for.
	e.do(c, "POST", "/settings/restart", url.Values{}, nil)
	if last := rs.asked[len(rs.asked)-1]; !last {
		t.Error("Restart now didn't restart now")
	}
	res, body = e.do(c, "GET", "/settings/restart", nil, nil)
	var st struct{ Pending bool }
	if err := json.Unmarshal([]byte(body), &st); err != nil || res.StatusCode != http.StatusOK || !st.Pending {
		t.Errorf("restart status: %d %s", res.StatusCode, body)
	}
	// Restarting is a POST: a cross-site one is refused.
	res, _ = e.do(c, "POST", "/settings/restart", url.Values{}, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site restart: %d", res.StatusCode)
	}
}
