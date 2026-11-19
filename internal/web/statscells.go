package web

import (
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
)

// The Stats pages' cells: each number with its line, or when the line
// starts.

// ovCell is one of the Stats page's numbers with its line, or (Wait) when
// the line starts while there isn't one yet. Also is what else there is
// beside the number, in a smaller type: "+ €12k".
type ovCell struct {
	N, Also string
	// AlsoKey and NoteKey are translation keys, not text: "since" before the
	// month in Also, "to decision" after the number. The words used to be
	// English literals here, which a Chinese page showed untranslated.
	AlsoKey, NoteKey string
	Chart            *lineChart
	Wait             string // "Nov 12"
}

// lineFrom is when a number saved daily since since gets its line: with
// the second day's save.
func lineFrom(since *time.Time) string {
	if since == nil {
		return ""
	}
	return since.AddDate(0, 0, 1).UTC().Format("Jan 2") // the next day's save
}

// The row's cells; nil for a number there's no line of, or the platform
// doesn't publish. Month numbers are the last finished month's, the others
// now's.

func (r platformStats) OpenCell() *ovCell {
	if r.OpenLine == nil {
		return nil
	}
	return &ovCell{N: num(r.Open), Chart: r.OpenLine}
}

func (r platformStats) AssetsCell() *ovCell {
	if r.AssetsLine == nil {
		return nil
	}
	return &ovCell{N: num(r.Assets), Chart: r.AssetsLine}
}

func (r platformStats) ScopeCell() *ovCell {
	return &ovCell{N: num(r.ScopeLast), Chart: &r.ScopeAdded}
}

func (r platformStats) LaunchCell() *ovCell {
	if r.Launched == nil {
		return nil
	}
	return &ovCell{N: num(r.LaunchedLast), Chart: r.Launched}
}

func (r platformStats) ReceivedCell() *ovCell {
	if r.ReceivedNow == nil {
		return nil
	}
	c := &ovCell{N: num(*r.ReceivedNow), Chart: r.Received, Wait: lineFrom(r.ReceivedSince)}
	if r.ReceivedEstimated {
		c.N = "≈" + c.N // partly worked out from monthly or total counts
	}
	return c
}

func (r platformStats) ValidCell() *ovCell {
	if r.Valid == nil {
		return nil
	}
	c := &ovCell{N: num(r.ValidLast), Chart: r.Valid}
	if r.ValidEst {
		c.N = "≈" + c.N
	}
	switch {
	case r.ValidPart:
		c.AlsoKey, c.Also = "since", r.ValidFrom // the month is only partly counted
	case r.ValidFrom != "":
		// last month wasn't counted at all: no number, and no line yet
		c.N, c.AlsoKey, c.Also, c.Chart = "", "counted from", r.ValidFrom, nil
	}
	return c
}

func (r platformStats) BountiesCell() *ovCell {
	if r.Bounties == nil {
		return nil
	}
	c := &ovCell{N: humanize.Money(r.BountiesLast, r.BountiesCurrency), Chart: r.Bounties}
	if r.BountiesEst {
		c.N = "≈" + c.N
	}
	var also []string
	for _, m := range r.BountiesAlso {
		also = append(also, "+ "+humanize.Money(m.Amount, m.Currency))
	}
	c.Also = strings.Join(also, " ")
	return c
}

func (r platformStats) PaidCell() *ovCell {
	if len(r.Paid) == 0 {
		return nil
	}
	c := &ovCell{N: humanize.Money(r.Paid[0].Amount, r.Paid[0].Currency), Wait: lineFrom(r.PaidSince), Chart: r.PaidLine}
	if r.PaidEstimated {
		c.N = "≈" + c.N
	}
	var also []string
	for _, m := range r.Paid[1:] {
		also = append(also, "+ "+humanize.Money(m.Amount, m.Currency))
	}
	c.Also = strings.Join(also, " ")
	return c
}

// ResolveCell is the median days to resolve, or where a platform gives no
// resolutions (Intigriti) its days to a decision; a platform's own page
// says which in the column's heading.
func (r platformStats) ResolveCell() *ovCell {
	if r.Resolve == nil && r.DecisionNow != nil {
		return &ovCell{N: humanize.Days(*r.DecisionNow), Chart: r.Decision, Wait: lineFrom(r.DecisionSince)}
	}
	if r.Resolve == nil {
		return nil
	}
	if r.ResolveLast == nil {
		return &ovCell{N: "none", Chart: r.Resolve} // none resolved last month
	}
	return &ovCell{N: humanize.Days(*r.ResolveLast), Chart: r.Resolve, Also: backlogNote(*r.ResolveLast)}
}

// backlogNote is what a month's median days to resolve says beside it:
// "backlog cleared" past backlogDays, else nothing. The Stats pages and a
// program's Stats tab both use it.
func backlogNote(days float64) string {
	if days > backlogDays {
		return "backlog cleared"
	}
	return ""
}

// backlogDays is a median to resolve that says a month closed mostly old
// reports: over half of them were older than this, so the number is about
// a clean-up more than how quickly reports are dealt with.
const backlogDays = 180

// ResolveCellNoted is ResolveCell with a decision time noted as such, for
// the Stats page's column, where the other platforms' are days to resolve.
func (r platformStats) ResolveCellNoted() *ovCell {
	c := r.ResolveCell()
	if c != nil && r.Resolve == nil {
		c.NoteKey = "to decision"
	}
	return c
}

// validEstimateFrom says what a platform's estimated valid reports per
// month come from (store.estimateValid, store.estimateFromLeaderboards).
func validEstimateFrom(platform string) string {
	switch platform {
	case "yeswehack":
		return " from reports received"
	case "intigriti":
		return " from the programs' monthly leaderboards"
	}
	return ""
}
