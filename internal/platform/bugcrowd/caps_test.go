package bugcrowd

import (
	"strings"
	"testing"
)

// The caps live in an attribute, so every quote arrives escaped and the object
// is nested: a plain regex on the raw page finds nothing usable. This is the
// shape measured from Bugcrowd own page on 2026-11-20.
func TestParseSubmissionCaps(t *testing.T) {
	page := `<div data-props="{&quot;submissionThrottleCaps&quot;:{&quot;reason&quot;:&quot;acceptance_rate&quot;,&quot;weeklyCap&quot;:6,&quot;weeklyRemaining&quot;:4,&quot;weeklyNextSlotAt&quot;:null,&quot;atCap&quot;:false,&quot;acceptanceRate&quot;:0.0,&quot;rateWindow&quot;:&quot;ninety_day&quot;,&quot;evaluationWindowDays&quot;:90,&quot;evaluatedCount&quot;:15,&quot;minEvaluated&quot;:5,&quot;policyVersion&quot;:3,&quot;threshold&quot;:50.0,&quot;earliestLiftAt&quot;:&quot;2026-10-15T05:17:23Z&quot;,&quot;openLimit&quot;:null},&quot;other&quot;:1}"></div>`
	caps, err := ParseSubmissionCaps(page)
	if err != nil {
		t.Fatal(err)
	}
	if caps.WeeklyCap != 6 || caps.WeeklyRemaining != 4 || caps.AtCap {
		t.Fatalf("caps = %+v", caps)
	}
	if caps.Threshold != 50 || caps.EvaluatedCount != 15 || caps.WindowDays != 90 {
		t.Fatalf("policy = %+v", caps)
	}
	if caps.EarliestLiftAt != "2026-10-15T05:17:23Z" {
		t.Fatalf("earliestLiftAt = %q", caps.EarliestLiftAt)
	}
}

// A page without the object is an error, not a zero-valued answer an operator
// would read as "no submissions left".
func TestParseSubmissionCapsWithoutTheObject(t *testing.T) {
	if _, err := ParseSubmissionCaps(`<html><body>nothing here</body></html>`); err == nil {
		t.Fatal("a page without the caps parsed as one")
	} else if !strings.Contains(err.Error(), "submissionThrottleCaps") {
		t.Fatalf("the error does not say what was missing: %v", err)
	}
}
