package web

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// A limit over a window means "the latest N": the page defaults to newest,
// and an API that returned the OLDEST N of the window answered
// /changes?since=30d with the oldest rows of it, which is the opposite of the
// question. ?order=oldest is still there for a replay.
func TestChangesAPIOrderDefaultsToNewest(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	// Two polls, each adding one domain: two changes, in a known order.
	first := platform.ScopeEntry{PlatformID: "1", InScope: true, AssetType: scope.AssetDomain, Identifier: "first.acme.example"}
	second := platform.ScopeEntry{PlatformID: "2", InScope: true, AssetType: scope.AssetDomain, Identifier: "second.acme.example"}
	for _, entries := range [][]platform.ScopeEntry{{first}, {first, second}} {
		e.fake.SetScope("acme", entries...)
		if _, err := e.syncer.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
			t.Fatal(err)
		}
	}

	read := func(path string) []int64 {
		_, body := e.do(c, "GET", path, nil, nil)
		var out struct {
			Changes []struct {
				ID int64 `json:"id"`
			} `json:"changes"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: %v %s", path, err, body)
		}
		vals := make([]int64, 0, len(out.Changes))
		for _, ch := range out.Changes {
			vals = append(vals, ch.ID)
		}
		return vals
	}

	newest := read("/api/v1/changes?since=7d")
	oldest := read("/api/v1/changes?since=7d&order=oldest")
	if len(newest) < 2 || len(oldest) < 2 {
		t.Fatalf("changes = %v / %v", newest, oldest)
	}
	if newest[0] != oldest[len(oldest)-1] || oldest[0] != newest[len(newest)-1] {
		t.Fatalf("the default is not newest-first: newest=%v oldest=%v", newest, oldest)
	}
}
