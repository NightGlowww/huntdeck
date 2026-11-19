package web

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// A program without bounties has its score multiplied by 0.75, so its parts do
// not weigh to the number shown. Measured on three live VDP programs (fiserv
// 43.2 from 57.56, allegion 40.9 from 54.54, ferrero 37.6 from 50.11), the
// ratio was 0.75 every time — and the panel said nothing about it.
//
// The first version of this test only looked at a paying program and asserted
// the note was absent, which cannot fail when the note is never rendered at all.
// It builds a no-bounty program now and asks for the note.
func TestNoBountyScoreIsExplained(t *testing.T) {
	e := newTestEnv(t)
	e.fake.SetPrograms(platform.Program{Handle: "vdp", Name: "VDP Co", URL: "https://platform.example/vdp",
		State: platform.StateOpen, OffersBounty: false})
	e.fake.SetScope("vdp", platform.ScopeEntry{PlatformID: "v1", InScope: true,
		AssetType: scope.AssetURL, Identifier: "api.vdp.example", BountyEligible: false})
	if _, err := e.syncer.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/programs/fake/vdp", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("program page: %d", res.StatusCode)
	}
	if !strings.Contains(body, "No bounties here") {
		t.Fatal("a program without bounties does not explain its score")
	}
	// And the numbers it prints have to be the ones it computed.
	score, mean := scoreAndMean(t, e, "vdp")
	if want := mean * 0.75; math.Abs(score-want) > 0.15 {
		t.Errorf("score %.1f is not 0.75 of the parts (%.2f)", score, mean)
	}
}

// scoreAndMean reads a program's score and the weighted mean of its parts.
func scoreAndMean(t *testing.T, e *testEnv, handle string) (score, mean float64) {
	t.Helper()
	_, body := e.do(e.client(), http.MethodGet, "/api/v1/programs/fake/"+handle, nil, nil)
	var p map[string]any
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	hunt, _ := p["hunt_score"].(map[string]any)
	score, _ = hunt["score"].(float64)
	parts, _ := hunt["parts"].([]any)
	var sum, weights float64
	for _, raw := range parts {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		v, _ := m["score"].(float64)
		w, _ := m["weight"].(float64)
		sum += v * w
		weights += w
	}
	if weights == 0 {
		t.Fatal("the program has no scored parts")
	}
	return score, sum / weights
}
