package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/huntmail/setup"
	"github.com/mdpsec/huntdeck/internal/overrides"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Settings' Accounts tab: the platforms' credentials and the other
// settings .env holds that can change while HuntDeck runs, saved
// encrypted over the environment's (internal/overrides). Secrets are
// write-only: the page says whether one is set, never what it is. Saving
// restarts the server in-process to use them, once no poll is running.

// Restarter restarts the server to apply saved settings.
type Restarter interface {
	// Restart asks for a restart: once no poll is running, or now (a
	// running poll stops, as at shutdown).
	Restart(now bool)
	// Pending reports whether a restart is waiting, and for which polls.
	Pending(ctx context.Context) (bool, []store.Run)
}

// accountsData is the Accounts tab.
type accountsData struct {
	Groups     []accountGroup
	Restart    *restartState // a restart waiting to apply saved settings
	Unreadable bool          // saved values can't be decrypted
	Tab        string        // "accounts", or "email" for HuntMail's box on its Setup tab
	// The status strip: how many groups there are, how many are ready, and how many carry saved values.
	Configured, Saved, Total int
}

type accountGroup struct {
	Key, Label string
	Platform   bool // a platform's, not the network's
	Fields     []overrides.FieldState
	Saved      bool     // some of its values are saved here: it can be reset
	Polled     bool     // the platform is configured (has credentials or polls public programs)
	SignIn     []signIn // how its credentials did, from Polling's row
	// Aside is the box's heading note; MailStatus how HuntMail answered
	// (its box only).
	Aside      string
	MailStatus *mailStatus
}

// mailStatus is how HuntMail answered just now, for its Accounts box.
type mailStatus struct {
	OK       bool
	Messages int64
	Newest   *time.Time
	Version  string // HuntMail's, when it says
	Problem  string
}

type restartState struct {
	Waiting []store.Run // the polls it waits for
}

// accountGroups are the groups' keys in order, platforms first.
var accountGroups = []string{config.GroupHackerOne, config.GroupBugcrowd, config.GroupIntigriti, config.GroupYesWeHack, config.GroupVulBox,
	config.GroupHuntMail, config.GroupNetwork, config.GroupPojia, config.GroupScan, config.GroupJev}

// accountPlatforms are the groups that are platforms, polled once saved.
var accountPlatforms = []string{config.GroupHackerOne, config.GroupBugcrowd, config.GroupIntigriti, config.GroupYesWeHack, config.GroupVulBox}

// settingsTabOf is the tab a group's box is on: HuntMail's on its own
// page's Setup tab ("email"), the rest on Settings › Accounts.
func settingsTabOf(group string) string {
	if group == config.GroupHuntMail {
		return "email"
	}
	return "accounts"
}

// accountsURL is the page a group's box is on, with msg to show there.
func accountsURL(group, msg string) string {
	if settingsTabOf(group) == "email" {
		return "/email/setup?msg=" + msg + "#acct-" + group
	}
	return "/settings?tab=accounts&msg=" + msg + "#acct-" + group
}

// accountsTab gathers the Accounts tab, or (tab "email") HuntMail's box
// for its Setup tab.
func (s *server) accountsTab(ctx context.Context, rows []platformRow, tab string) (*accountsData, error) {
	var fields []overrides.FieldState
	if s.Accounts == nil { // a read-only copy: every field, none set
		for _, f := range config.Editable {
			fields = append(fields, overrides.FieldState{Field: f, From: overrides.FromNone})
		}
	} else {
		var err error
		if fields, err = s.Accounts.Fields(ctx); err != nil {
			return nil, err
		}
	}
	d := &accountsData{Tab: tab}
	for _, key := range accountGroups {
		if settingsTabOf(key) != tab {
			continue
		}
		g := accountGroup{Key: key, Label: "Proxy and login browser", Aside: "every platform's traffic", Platform: slices.Contains(accountPlatforms, key)}
		switch {
		case g.Platform:
			g.Label, g.Aside = platformLabel(key), "not set up"
		case key == config.GroupHuntMail:
			g.Label, g.Aside = "HuntMail", "not set up"
			if s.Mail != nil {
				g.Aside, g.MailStatus = "for the Email page", s.mailStatus(ctx)
			}
		case key == config.GroupPojia:
			g.Label, g.Aside = "POJIA reports", "disclosed history"
		case key == config.GroupScan:
			g.Label, g.Aside = "Scan handoff", "a ScopeSentry instance"
		case key == config.GroupJev:
			g.Label, g.Aside = "Decision model", "asked before a handoff"
		}
		for _, f := range fields {
			if f.Group != key {
				continue
			}
			g.Fields = append(g.Fields, f)
			g.Saved = g.Saved || f.From == overrides.FromSaved || f.Unreadable
			d.Unreadable = d.Unreadable || f.Unreadable
		}
		if i := slices.IndexFunc(rows, func(r platformRow) bool { return r.Name == key }); i >= 0 {
			g.Polled, g.SignIn, g.Aside = true, rows[i].SignIn, "polled"
		}
		d.Groups = append(d.Groups, g)
		d.Total++
		if g.Polled {
			d.Configured++
		}
		if g.Saved {
			d.Saved++
		}
	}
	if s.Restart != nil {
		if pending, runs := s.Restart.Pending(ctx); pending {
			d.Restart = &restartState{Waiting: runs}
		}
	}
	return d, nil
}

// accountChanges reads a group's form: the fields whose value it changes.
// An empty secret keeps the saved one; its "clear_" box clears it. Text
// fields and switches change when they differ from the value in force, so
// untouched ones keep following .env.
func accountChanges(r *http.Request, fields []overrides.FieldState, group string) map[string]*string {
	changes := map[string]*string{}
	for _, f := range fields {
		if f.Group != group {
			continue
		}
		// A field the form did not submit is unchanged, not emptied. The page always
		// sends every field, so this only bites a script - and it bit: a POST that
		// carried one field cleared a platform's email, because an absent value and
		// an empty one were the same thing here.
		_, sent := r.PostForm[f.Name]
		_, clearing := r.PostForm["clear_"+f.Name]
		if !sent && !clearing {
			continue
		}
		v := strings.TrimSpace(r.PostFormValue(f.Name))
		switch f.Kind {
		case config.KindSecret:
			switch {
			case r.PostFormValue("clear_"+f.Name) != "":
				if f.Set || f.Unreadable {
					changes[f.Name] = new(string)
				}
			case v != "":
				changes[f.Name] = &v
			}
		case config.KindBool:
			if now := v != ""; now != f.On() {
				val := strconv.FormatBool(now)
				changes[f.Name] = &val
			}
		default:
			if v != f.Value || f.Unreadable {
				changes[f.Name] = &v
			}
		}
	}
	return changes
}

// formGroup is the group a form is for, from accountGroups.
func formGroup(r *http.Request) (string, bool) {
	i := slices.Index(accountGroups, r.PostFormValue("group"))
	if i < 0 {
		return "", false
	}
	return accountGroups[i], true
}

// saveAccounts saves one group's form.
func (s *server) saveAccounts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	group, ok := formGroup(r)
	if s.Accounts == nil || !ok {
		s.renderSettings(w, r, http.StatusNotFound, "Those settings can't be changed here.")
		return
	}
	fields, err := s.Accounts.Fields(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.applyAccounts(w, r, group, accountChanges(r, fields, group), "accounts-saved")
}

// resetAccounts drops a group's saved values, going back to .env's.
func (s *server) resetAccounts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	group, ok := formGroup(r)
	if s.Accounts == nil || !ok {
		s.renderSettings(w, r, http.StatusNotFound, "Those settings can't be changed here.")
		return
	}
	changes := map[string]*string{}
	for _, f := range config.Editable {
		if f.Group == group {
			changes[f.Name] = nil
		}
	}
	s.applyAccounts(w, r, group, changes, "accounts-reset")
}

// applyAccounts saves changes, restarts to use them and asks for the
// group's platform to be polled after the restart, so its sign-in shows.
func (s *server) applyAccounts(w http.ResponseWriter, r *http.Request, group string, changes map[string]*string, msg string) {
	if len(changes) == 0 {
		http.Redirect(w, r, accountsURL(group, "accounts-unchanged"), http.StatusSeeOther)
		return
	}
	err := s.Accounts.Save(r.Context(), changes)
	var invalid overrides.ValidationError
	switch {
	case errors.As(err, &invalid):
		msg := "Not saved: " + strings.ReplaceAll(invalid.Error(), "\n", "; ") + "."
		if settingsTabOf(group) == "email" {
			s.renderMailSetup(w, r, http.StatusBadRequest, mailSetupView{Name: setup.DefaultName}, msg)
		} else {
			s.renderSettings(w, r, http.StatusBadRequest, msg)
		}
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if slices.Contains(accountPlatforms, group) {
		if err := s.Accounts.PollAfterRestart(r.Context(), group); err != nil {
			s.Log.Warn("accounts: platform not marked to poll after the restart", "platform", group, "err", err)
		}
	}
	if s.Restart != nil {
		s.Restart.Restart(false)
	}
	http.Redirect(w, r, accountsURL(group, msg), http.StatusSeeOther)
}

// restartNow restarts without waiting for running polls.
func (s *server) restartNow(w http.ResponseWriter, r *http.Request) {
	if s.Restart == nil {
		s.renderSettings(w, r, http.StatusServiceUnavailable, "HuntDeck can't restart itself in this process.")
		return
	}
	s.Restart.Restart(true)
	http.Redirect(w, r, "/settings?tab=accounts", http.StatusSeeOther)
}

// restartStatus tells the Accounts tab's script whether the restart is
// still waiting (it reloads the page once it isn't).
func (s *server) restartStatus(w http.ResponseWriter, r *http.Request) {
	pending := false
	if s.Restart != nil {
		pending, _ = s.Restart.Pending(r.Context())
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"pending": pending})
}

// mailStatus asks HuntMail for its overview, briefly: Settings shows
// whether the address and key work.
func (s *server) mailStatus(ctx context.Context) *mailStatus {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	st, err := s.Mail.Stats(ctx, 0)
	if err != nil {
		return &mailStatus{Problem: mailProblem(err)}
	}
	return &mailStatus{OK: true, Messages: st.Messages, Newest: st.Newest, Version: st.Version}
}

// acctSection groups a credential field for the Accounts tab: the token a
// platform needs first, the optional website login, and the tuning nobody
// changes twice. The field names are the only signal there is.
func acctSection(name string) string {
	switch {
	case strings.HasSuffix(name, "_TOKEN"), strings.HasSuffix(name, "_USERNAME"):
		return "api"
	case strings.HasSuffix(name, "_EMAIL"), strings.HasSuffix(name, "_ACCOUNT"), strings.HasSuffix(name, "_PASSWORD"),
		strings.HasSuffix(name, "_TOTP_SECRET"), strings.HasSuffix(name, "_SESSION"):
		return "web"
	}
	return "advanced"
}
