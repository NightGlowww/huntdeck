package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/huntqueue"
	"github.com/mdpsec/huntdeck/internal/store"
)

// queueCmd prints HuntQueue: the targets ranked by "can I start today, and what
// is it worth per hour", with a hard gate for what cannot be started at all.
func queueCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	limit := fs.Int("limit", 20, "how many to print")
	candidates := fs.Int("candidates", 2000, "how many targets to rank (by target score) before taking the top")
	platform := fs.String("platform", "", "only this platform")
	kind := fs.String("kind", "", "only this kind of asset (domain, wildcard, url, ip, cidr)")
	paying := fs.Bool("paying", false, "only programs that pay bounties")
	fresh := fs.Bool("fresh", false, "skip the materialised table and compute now (for timing)")
	explain := fs.Bool("explain", false, "show what each factor did to the score (computes now: the saved table has no factors)")
	if err := fs.Parse(args); err != nil {
		if err != nil && args != nil && len(args) > 0 && args[0] == "-h" {
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	d, err := db.Open(ctx, dir+"/huntdeck.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck queue: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()

	s := store.New(d, time.Now)
	// The table first: it is what the app keeps up to date, and reading it is one
	// query instead of the candidate joins.
	// The materialised table carries scores, not the factors that made them, so
	// an explanation has to be computed: --explain implies the same path as
	// --fresh. Without this the flags parsed, the shortcut returned first, and
	// "huntdeck queue --explain" printed the plain table.
	if !*fresh && !*explain {
		if top, computed, err := s.HuntQueueTop(ctx, store.QueueFilter{Platform: *platform, Kind: *kind, Paying: *paying}, *limit, 0); err == nil && len(top) > 0 && time.Since(computed) < 48*time.Hour {
			fmt.Fprintf(stdout, "materialised %s ago\n", time.Since(computed).Round(time.Minute))
			printMaterialised(stdout, top)
			return 0
		}
	}
	rows, err := s.QueueCandidates(ctx, *candidates)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck queue: %v\n", err)
		return 1
	}

	now := time.Now()
	all := make([]queueRow, 0, len(rows))
	for _, in := range rows {
		res := huntqueue.Score(huntqueue.Inputs{
			ProgramValue: in.ProgramValue,
			PayoutUSD:    in.PayoutUSD,
			MedianDays:   in.SpeedDays,
			Disclosed365: in.Disclosed365,
			InScope:      in.InScope,
			AddedAt:      in.AddedAt,
			Now:          now,
			Flips:        in.Flips,
			NeedsCreds:   in.NeedsCreds,
		})
		all = append(all, queueRow{in, res})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].res.Score > all[j].res.Score })

	n := min(*limit, len(all))
	if *explain {
		printExplain(stdout, all[:n], now)
		return 0
	}
	printQueue(stdout, all[:n])
	return 0
}

// queueRow is one ranked target: what it is, and how it scored.
type queueRow struct {
	in  store.QueueInput
	res huntqueue.Result
}

func printQueue(w io.Writer, rows []queueRow) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tscore\tprogram\ttarget\twhy")
	for i, r := range rows {
		why := ""
		if n := min(2, len(r.res.Reasons)); n > 0 {
			why = strings.Join(huntqueue.English(r.res.Reasons[:n]), " · ")
		}
		fmt.Fprintf(tw, "%d\t%.1f\t%s/%s\t%s %s\t%s\n", i+1, r.res.Score, r.in.Platform, r.in.Handle, r.in.Kind, r.in.Value, why)
	}
	_ = tw.Flush()
}

// printMaterialised prints rows that came from the hunt_queue table.
func printMaterialised(w io.Writer, rows []store.HuntQueueRow) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tscore\tprogram\ttarget\twhy")
	for i, r := range rows {
		why := ""
		if len(r.Reasons) > 0 {
			why = strings.Join(huntqueue.English(r.Reasons[:min(2, len(r.Reasons))]), " · ")
		}
		fmt.Fprintf(tw, "%d\t%.1f\t%s/%s\t%s %s\t%s\n", i+1, r.Score, r.Platform, r.Handle, r.Kind, r.Value, why)
	}
	_ = tw.Flush()
}

// printExplain shows what each factor did to a row's score: the multiplier it
// applied and what that cost. The multipliers of a row multiply back to its
// score, so the biggest loss is the one to fix first.
func printExplain(w io.Writer, rows []queueRow, now time.Time) {
	for i, r := range rows {
		fmt.Fprintf(w, "%d  %.1f  %s/%s  %s %s\n", i+1, r.res.Score, r.in.Platform, r.in.Handle, r.in.Kind, r.in.Value)
		_, factors := huntqueue.Explain(huntqueue.Inputs{
			ProgramValue: r.in.ProgramValue, PayoutUSD: r.in.PayoutUSD, MedianDays: r.in.SpeedDays,
			Disclosed365: r.in.Disclosed365, InScope: r.in.InScope, AddedAt: r.in.AddedAt, Now: now,
			Flips: r.in.Flips, NeedsCreds: r.in.NeedsCreds,
		})
		for _, f := range factors {
			fmt.Fprintf(w, "    %-11s value %.2f × weight %.2f → × %.2f  (−%d%%)\n", f.Name, f.Value, f.Weight, f.Effect, int((1-f.Effect)*100+0.5))
		}
	}
}
