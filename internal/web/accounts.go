package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/config"
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
}

type accountGroup struct {
	Key, Label string
	Platform   bool // a platform's, not the network's
	Fields     []overrides.FieldState
	Saved      bool     // some of its values are saved here: it can be reset
	Polled     bool     // the platform is configured (has credentials or polls public programs)
	SignIn     []signIn // how its credentials did, from Polling's row
}

type restartState struct {
	Waiting []store.Run // the polls it waits for
}

// accountGroups are the groups' keys in order, platforms first.
var accountGroups = []string{config.GroupHackerOne, config.GroupBugcrowd, config.GroupIntigriti,
	config.GroupYesWeHack, config.GroupNetwork, config.GroupPojia, config.GroupScan, config.GroupJev}

// accountsTab gathers the Accounts tab.
func (s *server) accountsTab(ctx context.Context, rows []platformRow) (*accountsData, error) {
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
	d := &accountsData{}
	for _, key := range accountGroups {
		label := "Proxy and login browser"
		switch key {
		case config.GroupAI:
			label = "Optional model"
		case config.GroupPojia:
			label = "POJIA reports"
		case config.GroupScan:
			label = "Scan handoff"
		case config.GroupJev:
			label = "Decision model"
		case config.GroupNetwork:
			label = "Proxy and login browser"
		}
		g := accountGroup{Key: key, Label: label, Platform: key != config.GroupNetwork && key != config.GroupAI && key != config.GroupPojia && key != config.GroupScan && key != config.GroupJev}
		if g.Platform {
			g.Label = platformLabel(key)
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
			g.Polled, g.SignIn = true, rows[i].SignIn
		}
		d.Groups = append(d.Groups, g)
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
		http.Redirect(w, r, "/settings?tab=accounts&msg=accounts-unchanged#acct-"+group, http.StatusSeeOther)
		return
	}
	err := s.Accounts.Save(r.Context(), changes)
	var invalid overrides.ValidationError
	switch {
	case errors.As(err, &invalid):
		s.renderSettings(w, r, http.StatusBadRequest, "Not saved: "+strings.ReplaceAll(invalid.Error(), "\n", "; ")+".")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if group != config.GroupNetwork {
		if err := s.Accounts.PollAfterRestart(r.Context(), group); err != nil {
			s.Log.Warn("accounts: platform not marked to poll after the restart", "platform", group, "err", err)
		}
	}
	if s.Restart != nil {
		s.Restart.Restart(false)
	}
	http.Redirect(w, r, "/settings?tab=accounts&msg="+msg+"#acct-"+group, http.StatusSeeOther)
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

// acctSection groups a credential field for the Accounts tab: the token a
// platform needs first, the optional website login, and the tuning nobody
// changes twice. The field names are the only signal there is.
func acctSection(name string) string {
	switch {
	case strings.HasSuffix(name, "_TOKEN"), strings.HasSuffix(name, "_USERNAME"):
		return "api"
	case strings.HasSuffix(name, "_EMAIL"), strings.HasSuffix(name, "_PASSWORD"),
		strings.HasSuffix(name, "_TOTP_SECRET"), strings.HasSuffix(name, "_SESSION"):
		return "web"
	}
	return "advanced"
}
