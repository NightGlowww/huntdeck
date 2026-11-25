// Command huntdeck is the HuntDeck server and CLI.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	_ "time/tzdata" // time zone names work in a container without zoneinfo files

	"github.com/mdpsec/huntdeck/internal/version"
)

const usage = `HuntDeck — self-hosted bug bounty scope tracker

Usage:
  huntdeck <command> [flags]

Commands:
  serve        Run the web UI, API and scheduled polls (container default)
  poll         Poll platforms now                 [--platform h1] [--mode full|quick] [--limit N]
  export KIND  Print targets: wildcards|domains|urls|ips|cidrs
                                                  [--oos] [--platform h1] [--bounty] [--private] [--include-paused]
                                                  [--tag focus] [--include-ignored]
  stats [HANDLE...]  Fetch program stats now, without a poll (default: all programs)
                                                  [--platform h1]
  match HOST   Show which programs cover a host, IP or URL
  match -      Read hosts from stdin, print the in-scope ones   [-all]
  score        Recompute HuntScores now and list the best   [-n 25]
  status       Show what is stored and recent polls
  changes      Show recent changes                [--since 24h] [--limit 200]
  backup [--to FILE]  Write one file holding the database, the settings key and the sessions
                                                  [--data DIR] [--no-sessions] [--keep N]
  doctor       Look the data directory over: writability, schema, key, space  [--data DIR]
  scan-handoff  [--targets a,b,c] Send named targets to the scanner, one task per program
  scan-reconcile [--limit N] Read what the scanner says about the tasks HuntDeck created
  scan-clear  [--task ID,...] Forget the handoff records, so those targets are candidates
                again; the scanner own tasks are deleted from its own page
  account     Your standing per platform: HackerOne reputation and signal,
                Bugcrowd submission allowance, Intigriti reputation and rank
  bugcrowd-caps  What Bugcrowd allows this account to submit: the weekly cap,
                the acceptance rate against the threshold, the next reassessment
  restore --from FILE  Unpack such a file into a data directory (stop HuntDeck first)
                                                  [--data DIR] [--force] [--no-sessions] [--dry-run]
  reports      Read your own reports from the platforms now [--platform h1]
  seed export FILE  Write the public programs' data to FILE, for others to start from (.gz to compress)
  seed import FILE  Load such a file into a new, empty install (or put it in the data
                    directory as seed.db or seed.db.gz, and the first start loads it)
  migrate      Apply database migrations and exit
  healthcheck  Exit 0 if the local server is healthy (used by Docker)
  version      Print the version

Configuration is read from environment variables; see README.md.
Run "huntdeck <command> -h" for a command's flags.
`

// command runs with its arguments and returns an exit code.
type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{
	"serve":          noArgs(serve),
	"migrate":        noArgs(migrate),
	"healthcheck":    noArgs(healthcheck),
	"poll":           pollCmd,
	"export":         exportCmd,
	"match":          matchCmd,
	"stats":          statsFetchCmd,
	"score":          scoreCmd,
	"status":         statusCmd,
	"queue":          queueCmd,
	"intigriti":      intigritiCmd,
	"audit-texts":    auditTextsCmd,
	"triage":         triageCmd,
	"backtest":       backtestCmd,
	"compact":        compactCmd,
	"ai-check":       aiCheckCmd,
	"ai-extract":     aiExtractCmd,
	"changes":        changesCmd,
	"seed":           seedCmd,
	"backup":         backupCmd,
	"restore":        restoreCmd,
	"doctor":         doctorCmd,
	"account":        accountCmd,
	"scan-clear":     scanClearCmd,
	"scan-handoff":   scanHandoffCmd,
	"scan-reconcile": scanReconcileCmd,
	"bugcrowd-caps":  bugcrowdCapsCmd,
	"reports":        reportsCmd,
}

func main() {
	privateFiles()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	name, rest := args[0], args[1:]
	switch name {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "huntdeck", version.String())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "huntdeck: unknown command %q\n\n%s", name, usage)
		return 2
	}
	return cmd(rest, stdout, stderr)
}

func noArgs(fn func(stdout, stderr io.Writer) int) command {
	return func(args []string, stdout, stderr io.Writer) int {
		if len(args) > 0 {
			fmt.Fprintf(stderr, "huntdeck: unexpected arguments: %v\n", args)
			return 2
		}
		return fn(stdout, stderr)
	}
}

// parseFlags parses flags that may appear before or after positional
// arguments, returning the positionals. It returns ok=false with an exit
// code when parsing fails or -h was requested.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) (pos []string, code int, ok bool) {
	fs.SetOutput(stderr)
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, 0, false
			}
			return nil, 2, false
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, 0, true
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
