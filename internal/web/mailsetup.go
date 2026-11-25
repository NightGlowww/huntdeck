package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntmail/setup"
)

// Email › HuntMail › Setup: HuntMail's address and key (its Accounts
// box), and HuntDeck setting HuntMail up on the user's Cloudflare
// account, or updating it, from the bundle built in
// (internal/huntmail/setup). A run takes longer than a page may, so it
// runs in the background and the page follows it. The Cloudflare token is
// used for the run and dropped; the key it makes is saved encrypted, as if
// typed into the HuntMail box, and HuntDeck restarts to use it.

// mailSetupRestartDelay is how long after saving a new key HuntDeck waits
// to restart.
const mailSetupRestartDelay = 3 * time.Second

// mailSetupState is the latest run, while HuntDeck runs.
type mailSetupState struct {
	mu      sync.Mutex
	running bool
	started time.Time
	steps   []setup.Step
	err     string
	result  *setup.Result // without its key, once saved
	saved   bool          // the new address and key were saved
}

// mailSetupView is the page.
type mailSetupView struct {
	Accounts    *accountsData // HuntMail's address, key and domains
	CanSetUp    bool          // HuntDeck has a HuntMail to set up
	Version     string        // the bundle's
	Running     string        // the HuntMail version running now, when it says
	Configured  bool          // HuntDeck has a HuntMail address
	Domains     string        // the form's, as typed
	Name        string
	Replace     bool
	NewKey      bool
	Permissions [][2]string
	Problem     string // why the form wasn't taken

	Run     bool // a run started since HuntDeck did
	Busy    bool
	Started time.Time
	Steps   []setup.Step
	Err     string
	Result  *setup.Result
	Saved   bool
}

func (s *server) mailSetupPage(w http.ResponseWriter, r *http.Request) {
	s.renderMailSetup(w, r, http.StatusOK, mailSetupView{Name: setup.DefaultName}, "")
}

// renderMailSetup renders the Setup tab; flashErr is why its address and
// key weren't saved, "" when they weren't sent.
func (s *server) renderMailSetup(w http.ResponseWriter, r *http.Request, status int, v mailSetupView, flashErr string) {
	if s.Accounts == nil {
		s.renderError(w, r, http.StatusNotFound, "HuntMail's address and key can't be changed here: set HUNTMAIL_URL and HUNTMAIL_API_KEY in .env.")
		return
	}
	var err error
	if v.Accounts, err = s.accountsTab(r.Context(), nil, "email"); err != nil {
		s.serverError(w, r, err)
		return
	}
	if s.MailBundle != nil {
		v.CanSetUp, v.Version, v.Permissions = true, s.MailBundle.Version(), setup.Permissions
	}
	if cfg, err := s.Accounts.Config(r.Context()); err == nil {
		v.Configured = cfg.HuntMail.Enabled()
	}
	if s.Mail != nil {
		if st := s.mailStatus(r.Context()); st.OK {
			v.Running = st.Version
		}
	}
	st := &s.mailSetup
	st.mu.Lock()
	v.Run, v.Busy, v.Started, v.Steps, v.Err, v.Result, v.Saved = !st.started.IsZero(), st.running, st.started, st.steps, st.err, st.result, st.saved
	st.mu.Unlock()
	s.render(w, r, status, "mailsetup", pageData{Title: "HuntMail", Nav: "emailstats", Data: v, FlashErr: flashErr})
}

// mailSetupStatus tells the page's script whether the run is still going.
func (s *server) mailSetupStatus(w http.ResponseWriter, _ *http.Request) {
	s.mailSetup.mu.Lock()
	running, n, saved := s.mailSetup.running, len(s.mailSetup.steps), s.mailSetup.saved
	s.mailSetup.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"running":` + strconv.FormatBool(running) + `,"steps":` + strconv.Itoa(n) + `,"saved":` + strconv.FormatBool(saved) + "}\n"))
}

// startMailSetup checks the form and starts a run.
func (s *server) startMailSetup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if s.MailBundle == nil || s.Accounts == nil {
		s.renderError(w, r, http.StatusNotFound, "This HuntDeck can't set HuntMail up.")
		return
	}
	v := mailSetupView{
		Domains: strings.TrimSpace(r.PostFormValue("domains")),
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		Replace: r.PostFormValue("replace") != "",
		NewKey:  r.PostFormValue("newkey") != "",
	}
	if v.Name == "" {
		v.Name = setup.DefaultName
	}
	domains, err := setup.CheckDomains(strings.FieldsFunc(v.Domains, func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\r' || c == '\t' }))
	cfg, cerr := s.Accounts.Config(r.Context())
	switch {
	case cerr != nil:
		s.serverError(w, r, cerr)
		return
	case err != nil:
		v.Problem = err.Error() + "."
	case strings.TrimSpace(r.PostFormValue("token")) == "":
		v.Problem = "Enter a Cloudflare API token."
	case len(domains) == 0 && !cfg.HuntMail.Enabled():
		v.Problem = "Enter the domain to receive mail on."
	}
	if v.Problem != "" {
		s.renderMailSetup(w, r, http.StatusBadRequest, v, "")
		return
	}
	opts := setup.Options{
		Token: r.PostFormValue("token"), Domains: domains, Name: v.Name,
		ReplaceCatchAll: v.Replace, NewKey: v.NewKey, KnownURL: cfg.HuntMail.URL.Reveal(),
	}
	st := &s.mailSetup
	st.mu.Lock()
	if st.running {
		st.mu.Unlock()
		http.Redirect(w, r, "/email/setup", http.StatusSeeOther)
		return
	}
	st.running, st.started, st.steps, st.err, st.result, st.saved = true, time.Now(), nil, "", nil, false
	st.mu.Unlock()
	opts.Progress = func(steps []setup.Step) {
		st.mu.Lock()
		st.steps = steps
		st.mu.Unlock()
	}
	if s.MailSetupTest != nil {
		s.MailSetupTest(&opts)
	}
	// Not the request's context: the run outlives the page that started it.
	go s.runMailSetup(context.WithoutCancel(r.Context()), opts)
	http.Redirect(w, r, "/email/setup", http.StatusSeeOther)
}

// runMailSetup runs setup and saves what it made.
func (s *server) runMailSetup(ctx context.Context, opts setup.Options) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	res, err := setup.Run(ctx, s.MailBundle, opts)
	saved := false
	if err == nil && res.Key != "" {
		url, key := res.URL, res.Key
		res.Key = "" // the page never shows it, and nothing keeps it but the saved settings
		err = s.Accounts.Save(ctx, map[string]*string{"HUNTMAIL_URL": &url, "HUNTMAIL_API_KEY": &key})
		if err != nil {
			s.Log.Error("huntmail setup: saving the new key", "err", err)
			err = errors.New("HuntMail was set up, but its new key couldn't be saved: run this again with \"Make a new key\" ticked")
		} else {
			saved = true
		}
	}
	st := &s.mailSetup
	st.mu.Lock()
	st.running, st.result, st.saved = false, res, saved
	if err != nil {
		st.err = err.Error()
	}
	st.mu.Unlock()
	if err != nil {
		s.Log.Warn("huntmail setup stopped", "err", err) // Cloudflare's words, never the token or key
		return
	}
	s.Log.Info("huntmail set up", "version", res.Version, "domains", len(res.Domains), "new_key", saved)
	if saved && s.Restart != nil {
		// A moment first, so the page following the run sees it done and
		// moves to Settings, which follows the restart.
		time.AfterFunc(mailSetupRestartDelay, func() { s.Restart.Restart(false) })
	}
}
