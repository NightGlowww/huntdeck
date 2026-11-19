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
	}
	qd, err := s.QueueDays(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck backtest: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nqueue cuts available (days with a queue snapshot): %d\n", len(qd))
	if len(qd) < 2 {
		fmt.Fprintln(stdout, "the queue is snapshotted once a day from now on; a comparison needs a few days of it")
	} else {
		fmt.Fprintln(stdout, "the head of each day is stored, so a comparison can be asked for once the cuts line up")
	}
	return 0
}
