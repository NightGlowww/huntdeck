// Package huntqueue answers a different question from HuntScore: not "is this
// program worth it?" but "can I start on this target today, and how much is it
// worth per hour of effort?".
//
// It is deterministic and offline on purpose (docs/design/ai-layer.md §3): the
// inputs are numbers HuntDeck already stores, the formula is written down, and
// the tests pin the behaviour. A model that changes with a vendor's snapshot
// could not be trusted with a ranking a hunter uses every morning.
package huntqueue

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
)

// Weights of the geometric mean. They read as "how much a factor costs when it
// is ten times worse": w=0.15 means 0.1^0.15 ≈ 0.71, a 29% cut.
const (
	wQuality   = 0.30
	wMoney     = 0.25
	wSpeed     = 0.15
	wDup       = 0.15
	wFresh     = 0.10
	wReadiness = 0.05
)

// PayoutCapUSD is where the money factor saturates: $10,000 scores 1.0.
const PayoutCapUSD = 10000

// Gate is why a target is not in the queue today.
type Gate string

const (
	GateNone         Gate = ""
	GateInvite       Gate = "invite_pending"     // an invitation to accept first
	GateRequirements Gate = "requirements_unmet" // signal, EEA, application
	GatePaused       Gate = "paused_or_closed"
	GateIgnored      Gate = "ignored"
	GateNoScope      Gate = "scope_unavailable"
	GateRemoved      Gate = "removed"
)

// Inputs is one target on one program, as the store already has it.
type Inputs struct {
	ProgramValue float64 // HuntScore without the freshness part, 0..100
	PayoutUSD    float64 // best case for this target, in USD
	MedianDays   float64 // median days to accept; 0 when unknown
	Disclosed365 int     // disclosed reports in the last year
	InScope      int     // assets in scope in this program
	AddedAt      time.Time
	Now          time.Time
	Flips        int    // pause/reopen flips in the window
	NeedsCreds   bool   // credentials must be requested
	NoAutomation bool   // automation banned: hand testing only
	HandedOff    bool   // the scanner already has this target
	ScanState    string // what it said last: running, done, failed, gone, or "" unread

	// Platform ranks, 0..1, when the caller has them (1 = cleanest / fastest).
	// Zero means "no rank": the absolute value is used instead.
	DupRank   float64
	SpeedRank float64

	// Personal record on this program.
	ValidReports   int
	SubmittedTotal int
}

// Reason is one line of why a target scored as it did: a message key and its
// arguments, so every surface (page, API, CLI) renders it in its own language
// instead of storing English sentences.
type Reason struct {
	Key  string
	Args []string
}

// Result is one row of the queue.
type Result struct {
	Score   float64
	Gate    Gate
	Reasons []Reason
}

// personalPrior is the hit rate assumed before anyone has reported here.
const personalPrior = 0.25

// shrinkage: eight reports weigh as much as the prior.
const priorWeight = 8

// Score ranks one target. A gated target gets Gate set and Score 0: it is
// listed with its reason instead of being ranked.
func Score(in Inputs) Result {
	if gate := gateOf(in); gate != GateNone {
		return Result{Gate: gate, Reasons: []Reason{{Key: "gate " + string(gate)}}}
	}

	q := clamp01(in.ProgramValue / 100)
	m := money(in.PayoutUSD)
	s := speed(in)
	d := duplicates(in)
	f := freshness(in)
	r := readiness(in)
	p := personal(in)

	score := 100 * math.Pow(q, wQuality) * math.Pow(m, wMoney) * math.Pow(s, wSpeed) *
		math.Pow(d, wDup) * math.Pow(f, wFresh) * math.Pow(r, wReadiness) * p

	return Result{Score: round1(score), Reasons: reasons(in)}
}

func gateOf(in Inputs) Gate {
	// Only "there is nothing to hunt" is a gate here. A program whose score
	// minus its freshness part comes out negative (a fresh program scoring low)
	// is still huntable: it must score low, not disappear.
	if in.InScope <= 0 {
		return GateNoScope
	}
	return GateNone
}

// money saturates at PayoutCapUSD and is 0.5 at $100.
func money(usd float64) float64 {
	if usd <= 0 {
		return 0.35 // a program without a published bounty still pays sometimes
	}
	return clamp01(math.Log10(1+usd) / math.Log10(1+PayoutCapUSD))
}

// speed is 1.0 at 30 days or faster, 0.5 when nothing is known.
func speed(in Inputs) float64 {
	if in.SpeedRank > 0 {
		return clamp01(in.SpeedRank)
	}
	if in.MedianDays <= 0 {
		return 0.5
	}
	return clamp01(30 / math.Max(in.MedianDays, 1))
}

// duplicates: 30 disclosures over 120 assets is a crowded program.
func duplicates(in Inputs) float64 {
	if in.DupRank > 0 {
		return clamp01(in.DupRank)
	}
	assets := math.Max(10, float64(in.InScope))
	rate := 100 * float64(in.Disclosed365) / assets
	return clamp01(1 / (1 + rate/2))
}

// freshness halves every 21 days.
func freshness(in Inputs) float64 {
	if in.AddedAt.IsZero() || in.Now.IsZero() {
		return 0.5
	}
	days := in.Now.Sub(in.AddedAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	return clamp01(math.Pow(0.5, days/21))
}

// readiness: 1.0 open, 0.7 credentials to request, 0.5 hand testing only, and
// a further 0.8 when the program keeps flipping between paused and open: a
// target that may be gone tomorrow is not ready to start today.
func readiness(in Inputs) float64 {
	r := 1.0
	if in.NeedsCreds {
		r = 0.7
	}
	if in.Flips >= 4 {
		r *= 0.8
	}
	if in.NoAutomation {
		r = math.Min(r, 0.5)
	}
	return r
}

// personal is a multiplier that is constant until the hunter has a record
// here: with no history every program gets the same p, so the ordering is the
// global one.
func personal(in Inputs) float64 {
	rate := (float64(in.ValidReports) + priorWeight*personalPrior) / (float64(in.SubmittedTotal) + priorWeight)
	return 0.8 + 0.4*rate
}

func reasons(in Inputs) []Reason {
	out := make([]Reason, 0, 5)
	// First, so the two-reason row still shows it: a target the scanner already
	// holds is the thing a reader needs to know before anything else.
	if in.HandedOff {
		switch in.ScanState {
		case "done":
			out = append(out, Reason{Key: "already scanned: the scanner finished it"})
		case "failed":
			out = append(out, Reason{Key: "the scanner failed on it: hand it over again"})
		case "gone":
			out = append(out, Reason{Key: "the scanner no longer has it: hand it over again"})
		case "running", "queued", "paused":
			out = append(out, Reason{Key: "the scanner is on it now"})
		default:
			out = append(out, Reason{Key: "already handed to the scanner"})
		}
	}
	if in.PayoutUSD > 0 {
		out = append(out, Reason{Key: "pays up to $%s", Args: []string{moneyText(in.PayoutUSD)}})
	}
	switch {
	case in.MedianDays > 0:
		out = append(out, Reason{Key: "accepted in a median of %s days", Args: []string{fmt.Sprintf("%.0f", in.MedianDays)}})
	case in.SpeedRank > 0:
		out = append(out, Reason{Key: "acceptance speed not published: platform median used"})
	}
	if in.Disclosed365 > 0 && in.InScope > 0 {
		out = append(out, Reason{Key: "%s disclosures over %s assets: expect duplicates", Args: []string{fmt.Sprint(in.Disclosed365), fmt.Sprint(in.InScope)}})
	}
	if !in.AddedAt.IsZero() && !in.Now.IsZero() {
		days := int(in.Now.Sub(in.AddedAt).Hours() / 24)
		out = append(out, Reason{Key: "target added %s days ago", Args: []string{fmt.Sprint(max(days, 0))}})
	}
	if in.NoAutomation {
		out = append(out, Reason{Key: "automation banned: hand testing only"})
	} else if in.NeedsCreds {
		out = append(out, Reason{Key: "credentials have to be requested"})
	} else {
		out = append(out, Reason{Key: "nothing to arrange: start now"})
	}
	return out
}

// moneyText is the amount inside a queue reason: the page's own formatting, with
// the currency sign dropped because the reason says "pays up to $%s" itself.
//
// It used to have its own ladder - everything over a thousand became "%.1fk" -
// which rendered a three-million-dollar top bounty as "$3000.0k": correct
// arithmetic, and a number a reader has to divide to understand. One formatter
// now, so the queue and the pages cannot disagree again.
func moneyText(usd float64) string {
	return strings.TrimPrefix(humanize.Money(usd, "USD"), "$")
}

func clamp01(v float64) float64 { return math.Max(0.01, math.Min(1, v)) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// Render turns reasons into sentences: pass the page's translator, or English
// for the API, the CLI and MCP.
func Render(reasons []Reason, tr func(msg string, args ...string) string) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, tr(r.Key, r.Args...))
	}
	return out
}

// English renders reasons in English, for the surfaces that have no page
// language: the REST API, the CLI and MCP.
func English(reasons []Reason) []string {
	return Render(reasons, func(msg string, args ...string) string {
		out := msg
		for _, a := range args {
			out = strings.Replace(out, "%s", a, 1)
		}
		return out
	})
}

// Factor is one input's part in the score, for `huntdeck queue --explain`: the
// value the factor took (0..1) and how many of the score's points it carried.
// The weighted parts multiply, so "carried" is the share of the final score
// each factor is responsible for: contributions sum to the score.
type Factor struct {
	Name   string
	Value  float64 // the input, 0..1
	Weight float64
	// Effect is what the factor multiplied the score by (v^weight, 0..1): the
	// effects multiply back to the score, and 1-Effect is what it cost. They
	// are not added — a score is a product.
	Effect float64
}

// Explain breaks a score into its factors. A gated target has none.
func Explain(in Inputs) (float64, []Factor) {
	if gateOf(in) != GateNone {
		return 0, nil
	}
	q := clamp01(in.ProgramValue / 100)
	m := money(in.PayoutUSD)
	s := speed(in)
	d := duplicates(in)
	f := freshness(in)
	r := readiness(in)
	p := personal(in)
	score := 100 * math.Pow(q, wQuality) * math.Pow(m, wMoney) * math.Pow(s, wSpeed) *
		math.Pow(d, wDup) * math.Pow(f, wFresh) * math.Pow(r, wReadiness) * p

	// The multiplier each factor applied: v^weight. Multiplied together (and
	// by 100 and the personal factor) they give the score back exactly.
	effect := func(value, weight float64) float64 {
		if value <= 0 {
			return 0
		}
		return math.Pow(value, weight)
	}
	factors := []Factor{
		{"quality", q, wQuality, 0},
		{"money", m, wMoney, 0},
		{"speed", s, wSpeed, 0},
		{"duplicates", d, wDup, 0},
		{"freshness", f, wFresh, 0},
		{"readiness", r, wReadiness, 0},
		{"personal", p, 1, 0},
	}
	for i := range factors {
		factors[i].Effect = effect(factors[i].Value, factors[i].Weight)
	}
	return round1(score), factors
}
