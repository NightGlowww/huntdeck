package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mdpsec/huntdeck/internal/platform/bugcrowd"
	"github.com/mdpsec/huntdeck/internal/platform/hackerone"
	"github.com/mdpsec/huntdeck/internal/platform/intigriti"
)

// accountCmd prints the standing this install has on each platform: the numbers
// that decide what it may do next - HackerOne's reputation and signal, Bugcrowd's
// weekly submission allowance, Intigriti's reputation and rank.
//
// Each of them lives somewhere different (an API, a page in the researcher app,
// an attribute in a form's HTML), so the command asks each platform for what it
// has and says plainly when there is nothing to ask.
func accountCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("account", flag.ContinueOnError)
	platformName := fs.String("platform", "", "only this platform (e.g. h1); default every configured one")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "account: unexpected arguments: %v\n", pos)
		return 2
	}
	ctx, stop := signalContext()
	defer stop()
	app, ok := openApp(ctx, stderr, true)
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
		fmt.Fprintf(stderr, "account: %v\n", err)
		return 1
	}

	exit := 0
	for _, p := range ps {
		switch c := p.Platform.(type) {
		case *hackerone.Client:
			// The hacker API has no account resource: reputation, signal and impact
			// live only on hackerone.com itself, so there is nothing to ask for here.
			fmt.Fprintln(stdout, "hackerone  not read: the hacker API has no account endpoint")
		case *bugcrowd.Client:
			handle, err := app.store.OpenHandle(ctx, "bugcrowd")
			if err != nil || handle == "" {
				fmt.Fprintln(stdout, "bugcrowd   no open program to read the allowance from")
				continue
			}
			caps, err := c.SubmissionCaps(ctx, handle)
			if err != nil {
				fmt.Fprintf(stdout, "bugcrowd   %v\n", err)
				exit = 1
				continue
			}
			fmt.Fprintf(stdout, "bugcrowd   %d of %d submissions left this week · acceptance %.0f%% (threshold %.0f%%)\n",
				caps.WeeklyRemaining, caps.WeeklyCap, caps.AcceptanceRate, caps.Threshold)
		case *intigriti.Client:
			a, err := c.MyAccount(ctx)
			if err != nil {
				fmt.Fprintf(stdout, "intigriti  %v\n", err)
				exit = 1
				continue
			}
			fmt.Fprintf(stdout, "intigriti  %s · reputation %d · rank %d\n", a.UserName, a.Reputation, a.Rank)
		}
	}
	return exit
}
