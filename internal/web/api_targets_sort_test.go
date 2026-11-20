package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// sort=new is on the page and was silently ignored by the API: the rows came
// back in value order, which looks sorted to anyone who does not check the
// dates. The three the page offers are the three the API takes, and anything
// else is refused instead of ignored.
func TestTargetsAPISortIsHonouredOrRefused(t *testing.T) {
	e := newTestEnv(t)
	addDomain(t, e)
	c := e.client()

	for _, sort := range []string{"best", "new", "bounty", ""} {
		path := "/api/v1/targets?kind=domains&format=json&limit=10"
		if sort != "" {
			path += "&sort=" + sort
		}
		res, body := e.do(c, "GET", path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("sort=%q: %d %s", sort, res.StatusCode, body)
		}
	}

	res, body := e.do(c, "GET", "/api/v1/targets?kind=domains&format=json&sort=alphabetical", nil, nil)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "sort must be") {
		t.Fatalf("an unknown sort was accepted: %d %s", res.StatusCode, body)
	}

	// And sort=new really is newest first.
	_, body = e.do(c, "GET", "/api/v1/targets?kind=domains&format=json&limit=10&sort=new", nil, nil)
	var out struct {
		Targets []struct {
			AddedAt string `json:"added_at"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v %s", err, body)
	}
	for i := 1; i < len(out.Targets); i++ {
		if out.Targets[i-1].AddedAt < out.Targets[i].AddedAt {
			t.Fatalf("sort=new is not newest first: %v", out.Targets)
		}
	}
}
