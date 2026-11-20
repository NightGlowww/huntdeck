package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/platform/intigriti"
	"github.com/mdpsec/huntdeck/internal/store"
)

// intigritiCmd prints the researcher's own state on Intigriti: reputation, rank,
// identity check, pending invitations and applications, and the payouts the
// researcher API exposes.
//
// That state lives in two places. Reputation and rank are only in the web app
// (the researcher API has no notion of "me", its swagger lists six paths and
// none of them is a profile); payouts are on the API, marked BETA. So this needs
// both a token and a web login, exactly like the stats sync does.
func intigritiCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("intigriti", flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "data directory (default $HUNTDECK_DATA_DIR, else ./data)")
	asJSON := fs.Bool("json", false, "print everything as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	// Loaded the way the server loads: the token and the web login live in
	// Settings, not in the environment, so reading only the environment made this
	// command answer "not configured" on an instance that is configured.
	dir := *data
	if dir == "" {
		dir = os.Getenv("HUNTDECK_DATA_DIR")
	}
	if dir == "" {
		dir = "data"
	}
	d, err := db.Open(context.Background(), dir+"/huntdeck.db")
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck intigriti: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()
	cfg, err := overrides.New(store.New(d, time.Now), dir, config.OSSource).Config(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck intigriti: %v\n", err)
		return 1
	}
	host := loginBrowser(cfg, "", slog.New(slog.DiscardHandler))
	ps, err := buildPlatforms(cfg, host)
	if err != nil {
		fmt.Fprintf(stderr, "huntdeck intigriti: %v\n", err)
		return 1
	}
	var client *intigriti.Client
	for _, p := range ps {
		if c, ok := p.Platform.(*intigriti.Client); ok {
			client = c
		}
	}
	if client == nil {
		fmt.Fprintln(stderr, "huntdeck intigriti: not configured: set IT_TOKEN (and IT_EMAIL for the web-only figures)")
		return 1
	}
	// A browser login may have to run: four pages, a slow proxy, a challenge. The
	// login itself allows four minutes (weblogin.LoginBudget), so this must outlast
	// it - at sixty seconds the command reported `context deadline exceeded` for
	// every call while the proxy was answering in 200ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	out := struct {
		Account           *intigriti.Account      `json:"account,omitempty"`
		AccountError      string                  `json:"accountError,omitempty"`
		Invites           []intigriti.Invite      `json:"invites"`
		Applications      []intigriti.Application `json:"applications"`
		InvitesError      string                  `json:"invitesError,omitempty"`
		ApplicationsError string                  `json:"applicationsError,omitempty"`
		Payouts           []intigriti.Payout      `json:"payouts"`
		PayoutsError      string                  `json:"payoutsError,omitempty"`
	}{}
	if a, err := client.MyAccount(ctx); err != nil {
		out.AccountError = err.Error()
	} else {
		out.Account = &a
	}
	if inv, err := client.Invites(ctx); err != nil {
		out.InvitesError = err.Error()
	} else {
		out.Invites = inv
	}
	if apps, err := client.Applications(ctx); err != nil {
		out.ApplicationsError = err.Error()
	} else {
		out.Applications = apps
	}
	if pay, err := client.Payouts(ctx); err != nil {
		out.PayoutsError = err.Error()
	} else {
		out.Payouts = pay
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	if out.Account != nil {
		a := out.Account
		fmt.Fprintf(stdout, "%s · reputation %d · rank %d\n", a.UserName, a.Reputation, a.Rank)
		state := "not started"
		if a.Verified() {
			state = "checked"
		}
		fmt.Fprintf(stdout, "  identity: %s (state %d, %d attempt(s)) · profile %s · %s/%s\n",
			state, a.IdentityChecked, a.IdentityAttempts, doneWord(a.ProfileCompleted), a.Country, a.City)
	} else {
		fmt.Fprintf(stdout, "account: %s\n", out.AccountError)
	}
	fmt.Fprintf(stdout, "  invites: %s · applications: %s\n", countOr(out.InvitesError, len(out.Invites)), countOr(out.ApplicationsError, len(out.Applications)))
	if out.PayoutsError != "" {
		fmt.Fprintf(stdout, "  payouts: %s\n", out.PayoutsError)
	} else {
		fmt.Fprintf(stdout, "  payouts: %d\n", len(out.Payouts))
		for _, p := range out.Payouts {
			fmt.Fprintf(stdout, "    %.2f %s · %s · paid %s\n", p.Amount.Value, p.Amount.Currency, p.Status.Value, p.PaidAt)
		}
	}
	return 0
}

func doneWord(b bool) string {
	if b {
		return "complete"
	}
	return "incomplete"
}

// countOr shows a number, or the reason there is none: an empty list and a
// failed call must not print the same thing.
func countOr(errMsg string, n int) string {
	if errMsg != "" {
		return errMsg
	}
	return fmt.Sprintf("%d", n)
}
