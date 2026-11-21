package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestAPITokenBulkActions(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Password = "bulk-test-password" })
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, _ := e.do(c, http.MethodPost, "/login", url.Values{"password": {"bulk-test-password"}, "next": {"/api"}}, nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", res.StatusCode)
	}
	ctx := context.Background()
	ids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		id, err := e.store.CreateAPIToken(ctx, "api", fmt.Sprintf("token-%d", i), fmt.Sprintf("secret-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := e.store.RevokeAPITokenByKind(ctx, "api", ids[0]); err != nil {
		t.Fatal(err)
	}

	res, body := e.do(c, http.MethodGet, "/api", nil, nil)
	if res.StatusCode != http.StatusOK || !containsAll(body, `data-select-all`, `data-selbar`, `Delete selected revoked`) {
		t.Fatalf("API token table lacks bulk controls: %d", res.StatusCode)
	}

	res, _ = e.do(c, http.MethodPost, "/api/tokens/revoke-bulk", url.Values{"id": {fmt.Sprint(ids[1]), fmt.Sprint(ids[2])}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bulk revoke: %d", res.StatusCode)
	}
	res, body = e.do(c, http.MethodPost, "/api/tokens/delete-bulk", url.Values{"id": {fmt.Sprint(ids[0]), fmt.Sprint(ids[1]), fmt.Sprint(ids[2])}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bulk delete: %d", res.StatusCode)
	}
	rows, err := e.store.APITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == "api" {
			t.Fatalf("bulk delete left API token %d", row.ID)
		}
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}
