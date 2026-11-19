package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/store"
)

// backtestCmd measures a ranking against what the platforms actually paid: at a
// point in time, take the top N programs, and add up the bounties those programs
// accepted in the window after it. HuntQueue has to beat HuntScore by the
// margin written in docs/design/ai-layer.md §3.4, on enough cuts, or it does not
// replace it.
func backtestCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	top := fs.Int("top", 10, "how many programs a ranking picks")
	horizon := fs.Duration("horizon", 180*24*time.Hour, "how far after the cut to count bounties")
	if err := fs.Parse(args); err != nil {
		if len(args) > 0 && args[0] == "-h" {
			return 0
		}
		return 2
	}
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d, err := db.Open(ctx, dir+"/huntdeck.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()
	s := store.New(d, time.Now)

	days, err := s.ScoreDays(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cuts available (days with HuntScore): %d\n", len(days))
	if len(days) < 2 {
		fmt.Fprintln(stdout, "not enough history to measure anything yet: run HuntDeck for a while and come back")
		return 0
	}

	// The aggregate over days with a queue cut, so "which ranking captured
	// more" is one line the day the data exists, not arithmetic to redo.
	var hsTotal, qTotal float64
	qDays := 0
	for _, day := range days {
		cut, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		scores, err := s.ScoresOn(ctx, day)
		if err != nil {
			fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
			return 1
		}
		paid, err := s.BountyByProgram(ctx, cut, cut.Add(*horizon))
		if err != nil {
			fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
			return 1
		}
		sort.Slice(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
		captured, total := 0.0, 0.0
		for _, v := range paid {
			total += v
		}
		n := min(*top, len(scores))
		for _, p := range scores[:n] {
			captured += paid[p.ProgramID]
		}
		share := 0.0
		if total > 0 {
			share = 100 * captured / total
		}
		fmt.Fprintf(stdout, "%s  top-%d by HuntScore captures $%.0f of $%.0f (%.1f%%)\n", day, n, captured, total, share)
		hsTotal += captured
		// The same question for the queue: its targets, mapped to the programs
		// they belong to, since a bounty is paid to a program, not a target.
		if qrows, err := s.QueueOn(ctx, day); err == nil && len(qrows) > 0 {
			seen := map[int64]bool{}
			var order []int64
			for _, r := range qrows {
				if seen[r.ProgramID] {
					continue
				}
				seen[r.ProgramID] = true
				order = append(order, r.ProgramID)
				if len(order) == *top {
					break
				}
			}
			qcap := 0.0
			for _, id := range order {
				qcap += paid[id]
			}
			qshare := 0.0
			if total > 0 {
				qshare = 100 * qcap / total
			}
			fmt.Fprintf(stdout, "%s  top-%d by HuntQueue captures $%.0f of $%.0f (%.1f%%)\n", day, len(order), qcap, total, qshare)
			qTotal += qcap
			qDays++
		} else {
			fmt.Fprintf(stdout, "%s  no queue snapshot for this day\n", day)
		}
	}
	qd, err := s.QueueDays(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nqueue cuts available (days with a queue snapshot): %d\n", len(qd))
	if len(qd) < 2 {
		fmt.Fprintln(stdout, "one day of queue history so far: comparing days needs at least two, and the acceptance line needs eight")
	} else {
		if qDays > 0 {
			side := "even"
			switch {
			case qTotal > hsTotal:
				side = "HuntQueue captured more"
			case hsTotal > qTotal:
				side = "HuntScore captured more"
			}
			fmt.Fprintf(stdout, "over %d day(s) with a queue cut: HuntScore $%.0f, HuntQueue $%.0f — %s\n", qDays, hsTotal, qTotal, side)
		}
		fmt.Fprintln(stdout, "a bounty counted after a cut counts for both rankings; the acceptance line is in docs/design/ai-layer.md 3.4")
	}
	return 0
}
