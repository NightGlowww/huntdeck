package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The loss this pins: a POST that carried one field cleared another. A form
// always sends every field, so only a script hits it - and one did: saving a
// single value blanked a platform email, because an absent field and an empty
// one were the same thing.
func TestAccountsSaveLeavesFieldsItWasNotGivenAlone(t *testing.T) {
	e, _, _ := newAccountsEnv(t)
	c := e.client()

	res, _ := e.do(c, "POST", "/settings/accounts", url.Values{
		"group":       {"intigriti"},
		"IT_EMAIL":    {"someone@example.com"},
		"IT_PASSWORD": {"pw"},
		"IT_TOKEN":    {"tok-1"},
	}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	_, body := e.do(c, "GET", "/settings?tab=accounts", nil, nil)
	if !strings.Contains(body, `value="someone@example.com"`) {
		t.Fatal("the email was not saved")
	}

	// A later save that carries a different field must not touch it.
	res, _ = e.do(c, "POST", "/settings/accounts", url.Values{
		"group":   {"intigriti"},
		"IT_RATE": {"3"},
	}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("second save: %d", res.StatusCode)
	}
	_, body = e.do(c, "GET", "/settings?tab=accounts", nil, nil)
	if !strings.Contains(body, `value="someone@example.com"`) {
		t.Fatal("a save that did not mention the email cleared it")
	}
}
