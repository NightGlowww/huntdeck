package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
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
		ps, err = pickPlatforms(ps, "bugcrowd")
	}
	if err != nil {
		fmt.Fprintf(stderr, "bugcrowd-caps: %v\n", err)
		return 1
	}
	client, ok := ps[0].Platform.(*bugcrowd.Client)
	if !ok {
		fmt.Fprintln(stderr, "bugcrowd-caps: the bugcrowd platform is not a client")
		return 1
	}
	if *handle == "" {
		h, err := app.store.OpenHandle(ctx, "bugcrowd")
		if err != nil || h == "" {
			fmt.Fprintln(stderr, "bugcrowd-caps: no open bugcrowd program to read the page from; pass --engagement")
			return 1
		}
		*handle = h
	}
	caps, err := client.SubmissionCaps(ctx, *handle)
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
