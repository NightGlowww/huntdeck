package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func TestRequirementsShown(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, "GET", "/programs", nil, nil)
	for _, want := range []string{`<span class="chip req" title="3.0 or higher past-year Signal`, `>Signal 3.0</span>`,
		`ID check: Identity-verified`, `<span class="chip req" title="Researchers apply and must be accepted before testing">Apply</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("programs list lacks %q", want)
		}
	}
	_, body = e.do(c, "GET", "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, "Before you test") || !strings.Contains(body, "<b>Identity-verified hackers only") || strings.Contains(body, "Your profile") {
		t.Error("program page should list the requirements as they are")
	}
	// Nothing filters by a profile any more: can=1 is ignored.
	var out struct {
		Total int `json:"total"`
	}
	_, body = e.do(c, "GET", "/api/v1/programs?can=1", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 2 {
		t.Errorf("api can=1 = %s", body)
	}
}

func TestRecentlyResumed(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	acme := platform.Program{Handle: "acme", Name: "Acme Corp", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true}
	evil := platform.Program{Handle: "evil", Name: `<script>alert(1)</script>`, URL: "https://platform.example/evil", Private: true, State: platform.StatePaused}
	for _, state := range []string{platform.StatePaused, platform.StateOpen} {
		evil.State = state
		e.fake.SetPrograms(acme, evil)
		if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeQuick, Trigger: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	_, body := e.do(c, http.MethodGet, "/programs?resumed=7d&sort=resumed", nil, nil)
	if !strings.Contains(body, "1 program</span>") || !strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "Acme Corp") ||
		!strings.Contains(body, `<span class="chip resumed"`) || !strings.Contains(body, `<a class="af" href="/programs?sort=resumed" title="Remove this filter">Back open · 7 days`) {
		t.Errorf("programs resumed in 7d page is wrong")
	}
	_, body = e.do(c, http.MethodGet, "/", nil, nil)
	i := strings.Index(body, "<b>Status</b>")
	if i < 0 || !strings.Contains(body[i:], `<b class="add">1 reopened</b>`) || !strings.Contains(body[i:], "&lt;script&gt;") {
		t.Errorf("hunt board lacks the resumed program")
	}
}

func TestPolicyAndRewardChangesShowAsDiffs(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	acme := platform.Program{Handle: "acme", Name: "Acme Corp", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true,
		Policy: "## Program rules\n\n* Use your `@wearehackerone.com` alias\n* See [docs](https://docs.example.com)\n\nNo <b>DoS</b> please."}
	evil := platform.Program{Handle: "evil", Name: `<script>alert(1)</script>`, URL: "https://platform.example/evil", Private: true, State: platform.StateOpen}
	e.fake.SetPrograms(acme, evil)
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeQuick, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD", Rewards: []platform.RewardRow{
		{Scope: "*.acme.example", Low: platform.RewardRange{Max: 200}, Critical: platform.RewardRange{Max: 7500}},
		{Scope: "api.acme.example", Low: platform.RewardRange{Min: 100, Max: 400}, Critical: platform.RewardRange{Min: 10000, Max: 15000}},
	}})
	if _, _, err := e.syncer.RefreshStats(ctx, e.fake, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	list, _, err := e.store.Changes(ctx, store.ChangeFilter{Kinds: []string{store.ChangePolicyChanged, store.ChangeRewardsChanged}})
	// The new stats also dropped the reward notes.
	if err != nil || len(list) != 3 {
		t.Fatalf("rule changes = %d, %v", len(list), err)
	}
	var policy, rewards store.ChangeView
	for _, c := range list {
		switch {
		case c.Kind == store.ChangeRewardsChanged:
			rewards = c
		case c.Field == store.FieldPolicy:
			policy = c
		}
	}

	_, body := e.do(c, http.MethodGet, "/changes?group=rules", nil, nil)
	for _, want := range []string{">policy</span>", "Policy edited (&#43;1 line, −1 line)", ">rewards</span>",
		"Bounty table: *.acme.example critical: $5k → $7.5k", "Reward notes removed", fmt.Sprintf(`href="/changes/%d">view diff`, policy.ID)} {
		if !strings.Contains(body, want) {
			t.Errorf("changes page lacks %q", want)
		}
	}
	if _, body = e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil); !strings.Contains(body, fmt.Sprintf(`href="/changes/%d">view diff`, rewards.ID)) {
		t.Error("program history has no link to the rewards diff")
	}

	res, body := e.do(c, http.MethodGet, fmt.Sprintf("/changes/%d", policy.ID), nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("policy diff = %d", res.StatusCode)
	}
	for _, want := range []string{"Policy edited", "Acme Corp", `<div class="dl del" role="row">`, "<mark>&lt;img src=x</mark> <mark>onerror=alert(1)</mark>&gt;",
		"<mark>DoS&lt;/b&gt; please.</mark>", "+1 −1 lines"} {
		if !strings.Contains(body, want) {
			t.Errorf("policy diff lacks %q", want)
		}
	}
	if strings.Contains(body, "<b>DoS") || strings.Contains(body, "<img") {
		t.Error("policy diff isn't escaped")
	}
	_, body = e.do(c, http.MethodGet, fmt.Sprintf("/changes/%d", rewards.ID), nil, nil)
	for _, want := range []string{"Bounty table changed", "*.acme.example", `<span class="diff-old">$5k</span>`, `<span class="diff-new">$7.5k</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("rewards diff lacks %q", want)
		}
	}

	scopeChange, _, _ := e.store.Changes(ctx, store.ChangeFilter{Kinds: []string{store.ChangeScopeAdded}, Limit: 1})
	for _, path := range []string{"/changes/0", "/changes/x", "/changes/999999", fmt.Sprintf("/changes/%d", scopeChange[0].ID)} {
		if res, _ := e.do(c, http.MethodGet, path, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, res.StatusCode)
		}
	}
}

func TestHuntScore(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	_, body := e.do(c, http.MethodGet, "/", nil, nil)
	i := strings.Index(body, "<h2>Top picks</h2>")
	if i < 0 || !strings.Contains(body[i:], `href="/programs/fake/evil#huntscore" title="&lt;script&gt;alert(1)&lt;/script&gt;">&lt;script&gt;`) || !strings.Contains(body[i:], `<span class="pick-score" `) {
		t.Errorf("hunt board lacks top picks")
	}
	_, body = e.do(c, http.MethodGet, "/programs?sort=huntscore", nil, nil)
	if !strings.Contains(body, "c-hunt num") || strings.Count(body, `<td class="c-hunt num" data-label="HuntScore"><b class="score`) != 2 {
		t.Errorf("programs page lacks HuntScores")
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	for _, want := range []string{`id="huntscore"`, "Competition", "Balanced weighting"} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	if strings.Contains(body, "isn&#39;t among the picks") {
		t.Error("acme's requirements keep it out of the picks")
	}

	res, _ := e.do(c, http.MethodPost, "/settings/huntscore", url.Values{"preset": {"<b>x</b>"}}, nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown preset: %d", res.StatusCode)
	}
	res, _ = e.do(c, http.MethodPost, "/settings/huntscore", url.Values{"preset": {"quiet"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings?tab=you&msg=huntscore-saved" {
		t.Fatalf("save preset: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, body = e.do(c, http.MethodGet, "/settings?tab=you", nil, nil)
	if !strings.Contains(body, `value="quiet" checked`) || !strings.Contains(body, "Competition 50%") {
		t.Errorf("settings don't show the saved preset")
	}

	var list struct {
		Programs []struct {
			Handle string            `json:"handle"`
			Hunt   *store.HuntDetail `json:"hunt_score"`
		} `json:"programs"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/programs?sort=huntscore", nil, nil)
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Programs) != 2 || list.Programs[0].Hunt == nil || list.Programs[0].Hunt.Preset != "quiet" ||
		len(list.Programs[0].Hunt.Parts) != 4 || list.Programs[0].Hunt.Score < list.Programs[1].Hunt.Score {
		t.Errorf("API programs = %+v", list.Programs)
	}
	var one struct {
		Hunt *store.HuntDetail `json:"hunt_score"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	if err := json.Unmarshal([]byte(body), &one); err != nil {
		t.Fatal(err)
	}
	if one.Hunt == nil || one.Hunt.Gate != "" { // its Signal and ID check requirements don't gate it
		t.Errorf("API program hunt = %+v", one.Hunt)
	}
}

func TestIgnoreAndTags(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()

	if res, _ := e.do(c, http.MethodPost, "/programs/fake/acme/tags", url.Values{"tags": {"<b>bad</b>"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad tags: %d", res.StatusCode)
	}
	if res, _ := e.do(c, http.MethodPost, "/programs/fake/nope/tags", url.Values{"tags": {"x"}}, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown program: %d", res.StatusCode)
	}
	res, _ := e.do(c, http.MethodPost, "/programs/fake/acme/tags", url.Values{"tags": {"Focus, api"}}, nil)
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/programs/fake/acme") {
		t.Fatalf("save tags: %d %s", res.StatusCode, loc)
	}
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, `<a class="chip tag" href="/programs?tag=focus">focus</a>`) || !strings.Contains(body, `value="api, focus"`) {
		t.Errorf("program page lacks its tags")
	}
	_, body = e.do(c, http.MethodGet, "/programs?tag=focus&state=all", nil, nil)
	if !strings.Contains(body, "Acme Corp") || strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `<a class="fc on" href="/programs?state=all" aria-current="true"><span class="box" aria-hidden="true"></span><span class="fl">focus</span><em>1</em></a>`) {
		t.Errorf("tag filter on programs is wrong")
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards&tag=focus", nil, nil); body != "*.acme.example\n" {
		t.Errorf("tagged wildcards = %q", body)
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards&tag=nothing", nil, nil); body != "" {
		t.Errorf("wildcards for an unused tag = %q", body)
	}

	// Ignoring hides it from lists and exports, and keeps it out of the picks.
	res, _ = e.do(c, http.MethodPost, "/programs/fake/acme/ignore", url.Values{"ignored": {"1"}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("ignore: %d", res.StatusCode)
	}
	_, body = e.do(c, http.MethodGet, "/programs", nil, nil)
	if strings.Contains(body, "Acme Corp") {
		t.Error("an ignored program is listed")
	}
	_, body = e.do(c, http.MethodGet, "/programs?ignored=only", nil, nil)
	if !strings.Contains(body, "Acme Corp") || !strings.Contains(body, `<span class="chip ignored">ignored</span>`) {
		t.Error("ignored=only doesn't list it")
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards", nil, nil); body != "" {
		t.Errorf("an ignored program's wildcards are exported: %q", body)
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards&include_ignored=1", nil, nil); body != "*.acme.example\n" {
		t.Errorf("include_ignored wildcards = %q", body)
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, "Ignored by you") || !strings.Contains(body, ">Stop ignoring</button>") {
		t.Error("program page doesn't show it's ignored")
	}
	var prog struct {
		Ignored bool     `json:"ignored"`
		Tags    []string `json:"tags"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/acme", nil, nil)
	if err := json.Unmarshal([]byte(body), &prog); err != nil || !prog.Ignored || !slices.Equal(prog.Tags, []string{"api", "focus"}) {
		t.Errorf("API program = %+v, %v", prog, err)
	}

	e.do(c, http.MethodPost, "/programs/fake/acme/ignore", url.Values{"ignored": {"0"}}, nil)
	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards", nil, nil); body != "*.acme.example\n" {
		t.Errorf("after un-ignoring, wildcards = %q", body)
	}
	e.do(c, http.MethodPost, "/programs/fake/acme/tags", url.Values{"tags": {""}}, nil)
	_, body = e.do(c, http.MethodGet, "/programs", nil, nil)
	if strings.Contains(body, `name="tag"`) {
		t.Error("the tag filter shows with no tags left")
	}
}

func TestScopeExports(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/api/v1/programs/fake/acme/scope.burp.json", nil, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Disposition") != `attachment; filename="fake-acme.burp.json"` ||
		!strings.Contains(body, `"advanced_mode": true`) || !strings.Contains(body, `"host": "^([^./]+\\.)+acme\\.example$"`) ||
		!strings.Contains(body, `"host": "^admin\\.acme\\.example$"`) {
		t.Errorf("burp export: %d %v\n%s", res.StatusCode, res.Header, body)
	}
	res, body = e.do(c, http.MethodGet, "/api/v1/programs/fake/evil/scope.zap.context", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/xml") ||
		!strings.Contains(body, "<name>&lt;script&gt;alert(1)&lt;/script&gt;</name>") || !strings.Contains(body, "192.0.2.0/24") {
		t.Errorf("zap export: %d\n%s", res.StatusCode, body)
	}
	if res, _ = e.do(c, http.MethodGet, "/api/v1/programs/fake/nope/scope.burp.json", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown program: %d", res.StatusCode)
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, `href="/api/v1/programs/fake/acme/scope.burp.json"`) {
		t.Error("program page lacks the Burp export")
	}
}
