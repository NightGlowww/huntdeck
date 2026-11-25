package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// reportsCmd reads the user's own reports from each configured platform
// that has them, as polls do, and prints counts (never titles).
func reportsCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reports", flag.ContinueOnError)
	platformName := fs.String("platform", "", "only this platform (e.g. h1); default all configured")
	pos, code, ok := parseFlags(fs, args, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(stderr, "reports: unexpected arguments: %v\n", pos)
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
		fmt.Fprintf(stderr, "reports: %v\n", err)
		return 1
	}
	exit := 0
	for _, p := range ps {
		if rf, ok := p.Platform.(platform.ReportFetcher); !ok || !rf.ReportsConfigured() {
			if *platformName != "" {
				fmt.Fprintf(stderr, "reports %s: no login that can read reports\n", p.Name())
				exit = 1
			}
			continue
		}
		res, err := app.syncer.SyncReports(ctx, p.Platform)
		fmt.Fprintf(stdout, "%s: %d reports, %d changes, %d gone", p.Name(), res.Reports, res.Changes, res.Gone)
		if !res.Complete {
			fmt.Fprint(stdout, " (listing incomplete)")
		}
		fmt.Fprintln(stdout)
		if err != nil {
			fmt.Fprintf(stderr, "reports %s: %v\n", p.Name(), err)
			exit = 1
		}
	}
	return exit
}
