package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

// bugcrowdCaps prints what Bugcrowd allows this account to submit: the weekly
// cap and what is left of it, the acceptance rate against the threshold that
// lifts the cap, and when a reassessment is due.
//
// It reads one engagement's submission page, because that page is where Bugcrowd
// renders the numbers - there is no API for them, and the researcher API only
// says no once the cap is hit.
func bugcrowdCapsCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bugcrowd-caps", flag.ContinueOnError)
	handle := fs.String("engagement", "", "the engagement whose submission page to read (default: any open one)")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "bugcrowd-caps: unexpected arguments: %v\n", pos)
		return 2
	}
	ctx, stop := readOnlyContext(stderr)
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()

	caps, err := bugcrowdCaps(ctx, app.cfg, app.log, *handle)
	if err != nil {
		fmt.Fprintf(stderr, "bugcrowd-caps: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "submissions left: %d of %d this week\n", caps.WeeklyRemaining, caps.WeeklyCap)
	fmt.Fprintf(stdout, "acceptance rate: %.1f%% over %d days (%d evaluated, %d needed to judge), threshold %.0f%%\n",
		caps.AcceptanceRate, caps.WindowDays, caps.EvaluatedCount, caps.MinEvaluated, caps.Threshold)
	if caps.EarliestLiftAt != "" {
		fmt.Fprintf(stdout, "reassessed after: %s\n", caps.EarliestLiftAt)
	}
	if caps.AtCap {
		fmt.Fprintln(stdout, "at the cap: no submissions until the window moves")
	}
	return 0
}

// bugcrowdCaps reads the account's submission caps through the configured
// session. Both the command and the doctor use it, so they cannot drift.
func bugcrowdCaps(ctx context.Context, cfg config.Config, log *slog.Logger, handle string) (bugcrowd.SubmissionCaps, error) {
	var caps bugcrowd.SubmissionCaps
	host := loginBrowser(cfg, "", log)
	defer func() { _ = host.Close() }()
	ps, err := buildPlatforms(cfg, host)
	if err != nil {
		return caps, err
	}
	ps, err = pickPlatforms(ps, "bugcrowd")
	if err != nil {
		return caps, err
	}
	client, ok := ps[0].Platform.(*bugcrowd.Client)
	if !ok {
		return caps, fmt.Errorf("the bugcrowd platform is not a client")
	}
	return client.SubmissionCaps(ctx, handle)
}

// scanClearCmd forgets the handoffs HuntDeck recorded, so targets become
// candidates again. It is what makes a re-handoff possible after the scanner's
// own tasks are deleted: the scanner cannot be driven from here (its key has no
// delete tool), so the two halves are one command each.
// scanReconcileCmd reads what the scanner says about the tasks HuntDeck
// created and keeps the outcome: the one link the pipeline was missing.
func scanReconcileCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan-reconcile", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "how many of the newest tasks to read")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "scan-reconcile: unexpected arguments: %v\n", pos)
		return 2
	}
	ctx, stop := readOnlyContext(stderr)
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()
	if app.cfg.ScanURL == "" || app.cfg.ScanKey.Reveal() == "" {
		fmt.Fprintln(stderr, "scan-reconcile: no scanner configured; set it in Settings > Accounts > Scan handoff")
		return 1
	}
	client := &scopesentry.Client{BaseURL: app.cfg.ScanURL, Key: app.cfg.ScanKey.Reveal()}
	sum, err := scanhandoff.Reconcile(ctx, app.store, client, *limit, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "scan-reconcile: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "read %d of %d task(s): %d done, %d failed, %d gone\n",
		sum.Read, sum.Tasks, sum.Done, sum.Failed, sum.Gone)
	// What the handoff table actually holds, per state: the queue reads this
	// column, and "the page says unread" needs a number to argue with.
	if states, err := app.store.HandoffStates(ctx); err == nil {
		keys := make([]string, 0, len(states))
		for k := range states {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			name := k
			if name == "" {
				name = "unread"
			}
			parts = append(parts, name+"="+strconv.Itoa(states[k]))
		}
		fmt.Fprintf(stdout, "targets by state: %s\n", strings.Join(parts, " "))
	}
	return 0
}

func scanClearCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan-clear", flag.ContinueOnError)
	tasks := fs.String("task", "", "only this task id (comma separated); default every handoff")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "scan-clear: unexpected arguments: %v\n", pos)
		return 2
	}
	ctx, stop := readOnlyContext(stderr)
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()

	var ids []string
	if *tasks != "" {
		for _, id := range strings.Split(*tasks, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	n, err := app.store.ClearHandoffs(ctx, ids)
	if err != nil {
		fmt.Fprintf(stderr, "scan-clear: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cleared %d handoff(s); those targets are candidates again\n", n)
	return 0
}

// scanHandoffCmd hands named targets to the scanner: the operator's choice of
// what to scan, not the next batch off the top of the list. Everything else - a
// task per program, the scanner's own naming, the record that stops a re-handoff -
// is the same path the page and the scheduler use.
func scanHandoffCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan-handoff", flag.ContinueOnError)
	targets := fs.String("targets", "", "comma-separated targets to send (default: the next batch)")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "scan-handoff: unexpected arguments: %v\n", pos)
		return 2
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, false)
	if !ok {
		return 1
	}
	defer app.close()

	var wanted []string
	for _, t := range strings.Split(*targets, ",") {
		if t = strings.TrimSpace(t); t != "" {
			wanted = append(wanted, t)
		}
	}
	if app.cfg.ScanURL == "" || app.cfg.ScanKey.Reveal() == "" {
		fmt.Fprintln(stderr, "scan-handoff: no scanner configured; set it in Settings > Accounts > Scan handoff")
		return 1
	}
	client := &scopesentry.Client{BaseURL: app.cfg.ScanURL, Key: app.cfg.ScanKey.Reveal()}
	opt := scanhandoff.Options{Targets: wanted}
	// The page's button attaches the decision model's second opinion; the
	// command did not, so a batch the model would have blocked went out
	// anyway. Same question either way: it is a batch that can take the
	// scanner's host down.
	if app.cfg.JevKey.Reveal() != "" {
		opt.Opinion = scanhandoff.NewJevOpinion(&jev.Client{
			BaseURL: app.cfg.JevURL, Key: app.cfg.JevKey.Reveal(), Model: app.cfg.JevModel,
			Proxy: app.cfg.JevProxy, Provider: app.cfg.JevProvider,
			AccountID: app.cfg.JevAccountID, WorkspaceID: app.cfg.JevWorkspaceID,
		})
	}
	// The operator ran this command and named the targets: that is the
	// "someone should look" a confirm_first verdict asks for.
	opt.ConfirmedBy = "scan-handoff command"
	sum, err := scanhandoff.RunWith(ctx, app.store, client, true, opt)
	if err != nil {
		fmt.Fprintf(stderr, "scan-handoff: %v\n", err)
		if sum.Tasks > 0 {
			fmt.Fprintf(stdout, "created %d task(s) before stopping\n", sum.Tasks)
		}
		return 1
	}
	fmt.Fprintf(stdout, "created %d scan task(s) on node %s\n", sum.Tasks, sum.Node)
	return 0
}
