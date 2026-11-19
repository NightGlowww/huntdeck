package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func pollCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("poll", flag.ContinueOnError)
	platformName := fs.String("platform", "", "only this platform (e.g. h1); default all configured")
	mode := fs.String("mode", syncer.ModeFull, "full: refresh every scope the platform doesn't say is unchanged; quick: new programs and listed changes only")
	limit := fs.Int("limit", 0, "only process the first N programs (for trying things out; never removes anything)")
	force := fs.Bool("force-removals", false, "apply removals even if most programs (or a program's whole scope) would disappear at once")
	refetch := fs.Bool("refetch-scope", false, "full mode: download every program's scope, even ones the platform says are unchanged")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "poll: unexpected arguments: %v\n", pos)
		return 2
	}

	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()

	host := loginBrowser(app.cfg, "", app.log)
	defer func() { _ = host.Close() }()
	ps, err := buildPlatforms(app.cfg, host)
	if err == nil {
		ps, err = pickPlatforms(ps, *platformName)
	}
	if err != nil {
		fmt.Fprintf(stderr, "poll: %v\n", err)
		return 1
	}

	sched, err := openSchedule(ctx, app.store, ps)
	if err != nil {
		fmt.Fprintf(stderr, "poll: %v\n", err)
		return 1
	}
	exit := 0
	for _, p := range ps {
		if !sched.Enabled(p.Name()) {
			fmt.Fprintf(stderr, "poll %s: skipped, the platform is disabled in Settings\n", p.Name())
			continue
		}
		run, err := app.syncer.Poll(ctx, p.Platform, syncer.Options{Mode: *mode, Trigger: "manual", Limit: *limit, Concurrency: p.concurrency, ForceRemovals: *force, RefetchScope: *refetch})
		if err != nil {
			fmt.Fprintf(stderr, "poll %s: %v\n", p.Name(), err)
			exit = 1
			continue
		}
		fmt.Fprintln(stdout, syncer.FormatRun(run))
		if run.Status == store.RunFailed {
			exit = 1
		}
	}
	return exit
}

var exportKinds = map[string]string{
	"wildcards": scope.KindWildcard, "domains": scope.KindDomain, "urls": scope.KindURL,
	"ips": scope.KindIP, "cidrs": scope.KindCIDR,
}

func exportCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	oos := fs.Bool("oos", false, "export out-of-scope targets instead")
	platformName := fs.String("platform", "", "only this platform (e.g. h1)")
	bounty := fs.Bool("bounty", false, "only bounty-eligible targets on paying programs")
	private := fs.Bool("private", false, "only private programs")
	paused := fs.Bool("include-paused", false, "include paused and closed programs")
	ignored := fs.Bool("include-ignored", false, "include programs you ignore")
	tag := fs.String("tag", "", "only programs with this tag")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 || exportKinds[pos[0]] == "" {
		fmt.Fprintln(stderr, "usage: huntdeck export wildcards|domains|urls|ips|cidrs [flags]")
		return 2
	}

	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()

	f := store.TargetFilter{Kind: exportKinds[pos[0]], OutOfScope: *oos, BountyOnly: *bounty, PrivateOnly: *private, IncludePaused: *paused,
		IncludeIgnored: *ignored, Tag: strings.ToLower(strings.TrimSpace(*tag))}
	if *platformName != "" {
		f.Platform = canonicalPlatform(*platformName)
	}
	bw := bufio.NewWriter(stdout)
	err := app.store.EachTarget(ctx, f, func(v string) error {
		_, err := fmt.Fprintln(bw, v)
		return err
	})
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	return 0
}

func matchCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("match", flag.ContinueOnError)
	all := fs.Bool("all", false, `with "-": print every input with its verdict (in, out, none) and programs`)
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: huntdeck match <host|ip|url>\n       huntdeck match [-all] -   (one per line on stdin; prints the in-scope ones)")
		return 2
	}

	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()
	if pos[0] == "-" {
		return matchMany(ctx, app, os.Stdin, *all, stdout, stderr)
	}

	rules, progs, err := app.store.MatchRules(ctx, pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "match: %v\n", err)
		return 1
	}
	matches := scope.Match(rules, pos[0])
	if len(matches) == 0 {
		fmt.Fprintf(stdout, "%s: not in any stored program's scope\n", pos[0])
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	for _, m := range matches {
		p := progs[m.ProgramID]
		verdict := "IN"
		if !m.InScope {
			verdict = "OUT"
		}
		fmt.Fprintf(tw, "%s\t%s/%s\t%s\tvia %s\t%s\n", verdict, p.Platform, p.Handle, programFlags(p), m.Rule.Target.Value, p.URL)
	}
	_ = tw.Flush()
	return 0
}

// matchMany filters a list of hosts, IPs or URLs (tool output) down to
// the in-scope ones, or with all prints each one's verdict.
func matchMany(ctx context.Context, app *app, in io.Reader, all bool, stdout, stderr io.Writer) int {
	inputs, err := scope.ReadInputs(in, 0)
	if err != nil {
		fmt.Fprintf(stderr, "match: reading input: %v\n", err)
		return 1
	}
	rules, err := app.store.MatchRulesBulk(ctx, inputs)
	if err != nil {
		fmt.Fprintf(stderr, "match: %v\n", err)
		return 1
	}
	w := bufio.NewWriter(stdout)
	defer func() { _ = w.Flush() }()
	for _, input := range inputs {
		matches := scope.Match(rules.For(input), input)
		verdict := scope.Verdict(matches)
		if !all {
			if verdict == scope.VerdictIn {
				fmt.Fprintln(w, input)
			}
			continue
		}
		var progs []string
		for _, m := range matches {
			p := rules.Programs[m.ProgramID]
			side := "in"
			if !m.InScope {
				side = "out"
			}
			progs = append(progs, side+":"+p.Platform+"/"+p.Handle)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", verdict, input, strings.Join(progs, ","))
	}
	return 0
}

func programFlags(p store.ProgramRef) string {
	var f []string
	if p.Private {
		f = append(f, "private")
	}
	if p.State != "open" {
		f = append(f, p.State)
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, ",")
}

func statusCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if _, code, ok := parseFlags(fs, args, stderr); !ok {
		return code
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()

	sum, err := app.store.Summary(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "status: %v\n", err)
		return 1
	}
	if len(sum) == 0 {
		fmt.Fprintln(stdout, "No programs stored yet. Run: huntdeck poll")
	}
	for _, s := range sum {
		fmt.Fprintf(stdout, "%s: %d programs (%d private, %d open, %d with bounties), %d removed\n",
			s.Platform, s.Programs, s.Private, s.Open, s.Bounty, s.Removed)
		fmt.Fprintf(stdout, "  scope entries: %d in scope, %d out of scope\n", s.InScopeEntries, s.OutScopeEntries)
		var parts []string
		for _, k := range scope.Kinds {
			parts = append(parts, fmt.Sprintf("%d %ss", s.Targets[k], k))
		}
		fmt.Fprintf(stdout, "  in-scope targets: %s\n", strings.Join(parts, ", "))
	}

	runs, err := app.store.RecentRuns(ctx, 10)
	if err != nil {
		fmt.Fprintf(stderr, "status: %v\n", err)
		return 1
	}
	if len(runs) > 0 {
		zone := app.zone(ctx)
		fmt.Fprintln(stdout, "\nRecent polls:")
		tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
		for _, r := range runs {
			took := "running"
			if r.FinishedAt != nil {
				took = r.FinishedAt.Sub(r.StartedAt).Round(time.Second).String()
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%d programs\t%d scopes\t%d changes\t%d requests\t%s\t%s\n",
				r.StartedAt.In(zone).Format("2006-01-02 15:04 MST"), r.Platform, r.Mode, r.DisplayStatus(), r.ProgramsSeen,
				r.ScopesFetched, r.Changes, r.Requests, took, r.Error)
		}
		_ = tw.Flush()
	}

	if days, err := app.store.ScoreDays(ctx); err == nil && len(days) > 0 {
		fmt.Fprintf(stdout, "\nScore history: %d day(s), newest %s (the backtest wants 8)\n", len(days), days[len(days)-1])
	}
	if at, err := app.store.HuntQueueBuiltAt(ctx); err == nil && !at.IsZero() {
		fmt.Fprintf(stdout, "Queue: built %s\n", app.store.Now().Sub(at).Round(time.Second))
	}
	return 0
}

func changesCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	since := fs.Duration("since", 24*time.Hour, "how far back to look")
	limit := fs.Int("limit", 200, "maximum changes to show")
	if _, code, ok := parseFlags(fs, args, stderr); !ok {
		return code
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()

	changes, _, err := app.store.Changes(ctx, store.ChangeFilter{Since: time.Now().Add(-*since), Limit: *limit})
	if err != nil {
		fmt.Fprintf(stderr, "changes: %v\n", err)
		return 1
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "No changes in the last %s.\n", *since)
		return 0
	}
	zone := app.zone(ctx)
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	for _, c := range changes {
		detail := c.Field
		switch {
		case c.Kind == store.ChangePolicyChanged || c.Kind == store.ChangeRewardsChanged:
			detail += fmt.Sprintf(" (diff: /changes/%d)", c.ID) // whole texts don't fit a terminal line
		case c.OldValue != "" || c.NewValue != "":
			detail += ": " + oneLine(c.OldValue) + " → " + oneLine(c.NewValue)
		}
		fmt.Fprintf(tw, "%s\t%s/%s\t%s\t%s\t%s\n", c.OccurredAt.In(zone).Format("2006-01-02 15:04 MST"),
			c.Program.Platform, c.Program.Handle, programFlags(c.Program), c.Kind, detail)
	}
	_ = tw.Flush()
	return 0
}

// scoreCmd recomputes every program's HuntScore now (polls do it too) and
// prints the best.
func scoreCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	n := fs.Int("n", 25, "how many programs to print")
	if _, code, ok := parseFlags(fs, args, stderr); !ok {
		return code
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
	if !ok {
		return 1
	}
	defer app.close()
	if err := huntscore.Refresh(ctx, app.store, time.Now()); err != nil {
		fmt.Fprintf(stderr, "score: %v\n", err)
		return 1
	}
	if err := app.store.RefreshHuntQueue(ctx, time.Now()); err != nil {
		fmt.Fprintf(stderr, "queue: %v\n", err)
	}
	picks, err := app.store.TopHunts(ctx, *n)
	if err != nil {
		fmt.Fprintf(stderr, "score: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCORE\tFRESH\tCOMP\tPAY\tRESP\tCONF\tPROGRAM\tTOP REASON")
	for _, p := range picks {
		parts := map[string]store.HuntPart{}
		for _, pt := range p.Detail.Parts {
			parts[pt.Key] = pt
		}
		fmt.Fprintf(tw, "%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%s\t%s/%s\t%s\n", p.Detail.Score, parts[huntscore.Fresh].Score,
			parts[huntscore.Competition].Score, parts[huntscore.Payout].Score, parts[huntscore.Response].Score,
			p.Detail.Confidence, p.Platform, p.Handle, oneLine(p.Detail.TopReason()))
	}
	_ = tw.Flush()
	return 0
}

// oneLine shortens multi-line values (e.g. instructions) for terminal output.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:57]) + "..."
	}
	return s
}

// statsFetchCmd refreshes public program stats without a scope poll, e.g.
// to look at a few programs before the next full poll collects the rest.
func statsFetchCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	platformName := fs.String("platform", "hackerone", "platform whose programs to fetch (e.g. h1)")
	handles, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}

	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()

	host := loginBrowser(app.cfg, "", app.log)
	defer func() { _ = host.Close() }()
	ps, err := buildPlatforms(app.cfg, host)
	if err == nil {
		ps, err = pickPlatforms(ps, *platformName)
	}
	if err != nil {
		fmt.Fprintf(stderr, "stats: %v\n", err)
		return 1
	}
	exit := 0
	for _, p := range ps {
		fetched, failed, err := app.syncer.RefreshStats(ctx, p.Platform, handles)
		fmt.Fprintf(stdout, "%s: program stats fetched for %d programs", p.Name(), fetched)
		if failed > 0 {
			fmt.Fprintf(stdout, ", %d failed", failed)
		}
		fmt.Fprintln(stdout)
		if err != nil {
			fmt.Fprintf(stderr, "stats %s: %v\n", p.Name(), err)
			exit = 1
		}
	}
	return exit
}
