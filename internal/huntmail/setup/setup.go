// Package setup sets HuntMail up on the user's own Cloudflare account, or
// updates it, from the bundle built into HuntDeck: what HuntMail's
// scripts/setup.sh does with wrangler, done with Cloudflare's API so
// nothing else needs installing. It creates (or finds) the D1 database,
// applies the migrations it hasn't had, uploads the Worker with its
// settings, makes the API key and private base path, schedules its cron,
// turns its workers.dev address on, and points each domain's catch-all at
// it.
//
// It looks before it changes anything: a domain whose catch-all already
// goes somewhere else, or a HuntMail whose key HuntDeck doesn't have,
// stops it before the first change, unless the user said to replace them.
// Every step finds what's there before it creates, so running it again
// carries on or updates, never duplicates.
//
// The Cloudflare token is used for the run and dropped: it's never saved,
// logged or put in an error. The key and base path it makes are returned
// once, for HuntDeck to save encrypted.
//
// Request budget: about 15 Cloudflare API calls, plus 3 per domain and 2
// per migration, at most 4 a second; then up to 12 requests to the new
// HuntMail's health check while its address comes up.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// DefaultAPI is Cloudflare's API.
const DefaultAPI = "https://api.cloudflare.com/client/v4"

// DefaultName is the Worker's (and its database's) name.
const DefaultName = "huntmail"

// Permissions are what the token needs, as Cloudflare's token page names
// them: the same as HuntMail's own setup.
var Permissions = [][2]string{
	{"Account", "Workers Scripts: Edit"},
	{"Account", "D1: Edit"},
	{"Account", "Account Settings: Read"},
	{"Zone", "Zone: Read"},
	{"Zone", "Zone Settings: Edit"},
	{"Zone", "Email Routing Rules: Edit"},
}

// Options says what to set up.
type Options struct {
	Token   string
	Domains []string // domains to point at HuntMail; none to only update it
	Name    string   // DefaultName if ""
	// ReplaceCatchAll lets it replace a domain's catch-all that goes
	// somewhere else (forwards, or another Worker).
	ReplaceCatchAll bool
	// NewKey makes a new API key and base path for a HuntMail that already
	// has them: anything using the old ones stops working.
	NewKey bool
	// KnownURL is the HuntMail address HuntDeck has now, if any: a HuntMail
	// at that address keeps its key.
	KnownURL string

	API      string                                           // DefaultAPI if ""
	HTTP     *httpclient.Client                               // for Cloudflare; tests
	Health   *httpclient.Client                               // for HuntMail's health check; tests
	HealthAt string                                           // tests: the health check's origin instead of workers.dev
	Sleep    func(ctx context.Context, d time.Duration) error // tests
	Progress func([]Step)                                     // every step so far, after each change
}

// Step is one step of a run, for the page that follows it.
type Step struct {
	Title  string
	Detail string
	State  string // StepRunning, StepDone or StepFailed
}

// Step states.
const (
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
)

// Result is what a run did.
type Result struct {
	Version string   // the HuntMail version now deployed
	Origin  string   // https://<name>.<subdomain>.workers.dev
	URL     string   // the address with its base path: only when made now
	Key     string   // the API key: only when made now
	Domains []string // every domain HuntMail now has
	Healthy bool     // it answered its health check
}

// CatchAllError: a domain's catch-all goes somewhere else, and the run
// wasn't told to replace it.
type CatchAllError struct {
	Domain string
	Does   string // e.g. "forward to a@example.com"
}

func (e *CatchAllError) Error() string {
	return fmt.Sprintf("%s's catch-all already does something: it %s. Tick \"Replace an existing catch-all\" to point it at HuntMail instead.", e.Domain, e.Does)
}

// ErrKeyUnknown: a HuntMail of that name already runs on the account, and
// HuntDeck doesn't have its key.
var ErrKeyUnknown = errors.New("a HuntMail of that name already runs on this Cloudflare account, and HuntDeck doesn't have its key. " +
	"Enter its address and API key in Settings › Accounts › HuntMail, or tick \"Make a new key\" (whatever uses the old key stops working)")

var (
	workerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	domainName = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// CheckDomains tidies a list of domains as typed: lowercase, no
// duplicates, each a domain name.
func CheckDomains(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if !domainName.MatchString(d) {
			return nil, fmt.Errorf("%q isn't a domain name", clip(d, 80))
		}
		out = append(out, d)
	}
	return out, nil
}

// run is one run's state.
type run struct {
	o     Options
	b     *Bundle
	cf    *cloudflare
	steps []Step

	account string
	zones   []zone
	exists  bool              // the Worker is there already
	vars    map[string]string // its plain_text bindings now
	dbID    string            // its database, from its binding
	secrets []string          // the names of its secrets
	sub     string            // the account's workers.dev subdomain
	newKey  bool              // make the API key and base path
	base    string            // the base path, when known
	key     string            // the API key, when made now
}

type zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// Run sets HuntMail up, or updates it. On an error, the steps done stay
// done: running it again carries on.
func Run(ctx context.Context, b *Bundle, o Options) (*Result, error) {
	if b == nil {
		return nil, ErrNoBundle
	}
	if o.Name == "" {
		o.Name = DefaultName
	}
	if !workerName.MatchString(o.Name) {
		return nil, errors.New("the name must be lowercase letters, digits and dashes")
	}
	if strings.TrimSpace(o.Token) == "" {
		return nil, errors.New("enter a Cloudflare API token")
	}
	if o.API == "" {
		o.API = DefaultAPI
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	if o.Health == nil {
		o.Health = httpclient.New(httpclient.Options{MaxAttempts: 1, Timeout: 10 * time.Second, MaxBodyBytes: 1 << 16})
	}
	r := &run{o: o, b: b, cf: newCloudflare(o.API, strings.TrimSpace(o.Token), o.HTTP)}
	return r.run(ctx)
}

func (r *run) run(ctx context.Context) (*Result, error) {
	// Look first: nothing below changes anything.
	if err := r.step(ctx, "Check the token and domains", r.findZones); err != nil {
		return nil, err
	}
	if len(r.zones) > 0 {
		if err := r.step(ctx, "Check the domains' catch-all", r.checkCatchAlls); err != nil {
			return nil, err
		}
	}
	if err := r.step(ctx, "Look for HuntMail on the account", r.findWorker); err != nil {
		return nil, err
	}
	if err := r.step(ctx, "Find the workers.dev address", r.findSubdomain); err != nil {
		return nil, err
	}

	// Then change, each step safe to run again.
	for _, s := range []struct {
		title string
		fn    func(context.Context) (string, error)
	}{
		{"Database", r.ensureDatabase},
		{"Database migrations", r.migrate},
		{"Upload HuntMail " + r.b.Version(), r.uploadWorker},
		{"API key and private address", r.putSecrets},
		{"Schedule", r.schedule},
		{"workers.dev address", r.enableSubdomain},
	} {
		if err := r.step(ctx, s.title, s.fn); err != nil {
			return nil, err
		}
	}
	for _, z := range r.zones {
		if err := r.step(ctx, "Email Routing on "+z.Name, func(ctx context.Context) (string, error) { return r.route(ctx, z) }); err != nil {
			return nil, err
		}
	}

	res := &Result{Version: r.b.Version(), Origin: r.origin(), Domains: splitList(r.vars["DOMAINS"])}
	if r.newKey {
		res.URL = r.origin() + "/" + r.base
	}
	res.Key = r.key
	if r.base != "" {
		_ = r.step(ctx, "Check HuntMail answers", func(ctx context.Context) (string, error) {
			res.Healthy = r.waitHealthy(ctx)
			if !res.Healthy {
				return "not answering yet: a new workers.dev address can take a few minutes", nil
			}
			return "it answers", nil
		})
	}
	return res, nil
}

// step runs one step, reporting it before and after.
func (r *run) step(ctx context.Context, title string, fn func(context.Context) (string, error)) error {
	r.steps = append(r.steps, Step{Title: title, State: StepRunning})
	r.report()
	detail, err := fn(ctx)
	s := &r.steps[len(r.steps)-1]
	if err != nil {
		s.State, s.Detail = StepFailed, err.Error()
	} else {
		s.State, s.Detail = StepDone, detail
	}
	r.report()
	return err
}

func (r *run) report() {
	if r.o.Progress != nil {
		r.o.Progress(slices.Clone(r.steps))
	}
}

func (r *run) origin() string { return "https://" + r.o.Name + "." + r.sub + ".workers.dev" }

func (r *run) findZones(ctx context.Context) (string, error) {
	for _, d := range r.o.Domains {
		var zs []zone
		if err := r.cf.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(d), nil, &zs, "find "+d, "Zone Read"); err != nil {
			return "", err
		}
		if len(zs) == 0 {
			return "", fmt.Errorf("%s isn't on this Cloudflare account, or the token's Zone Resources don't include it", d)
		}
		z := zs[0]
		if r.account != "" && z.Account.ID != r.account {
			return "", errors.New("the domains are on different Cloudflare accounts: set HuntMail up on one account at a time")
		}
		r.account = z.Account.ID
		r.zones = append(r.zones, z)
	}
	if r.account == "" { // only updating: the token's one account
		var accts []struct {
			ID string `json:"id"`
		}
		if err := r.cf.call(ctx, http.MethodGet, "/accounts?per_page=50", nil, &accts, "list the accounts", "Account Settings Read"); err != nil {
			return "", err
		}
		if len(accts) != 1 {
			return "", errors.New("enter a domain, so HuntDeck knows which Cloudflare account to use")
		}
		r.account = accts[0].ID
	}
	if len(r.zones) == 0 {
		return "the token works", nil
	}
	return "the token works, and sees " + strings.Join(r.o.Domains, ", "), nil
}

type catchAll struct {
	Enabled bool `json:"enabled"`
	Actions []struct {
		Type  string   `json:"type"`
		Value []string `json:"value"`
	} `json:"actions"`
}

func (r *run) checkCatchAlls(ctx context.Context) (string, error) {
	for _, z := range r.zones {
		var c catchAll
		err := r.cf.call(ctx, http.MethodGet, "/zones/"+z.ID+"/email/routing/rules/catch_all", nil, &c, "read "+z.Name+"'s catch-all", "Email Routing Rules Edit")
		if err != nil && !notFound(err) {
			return "", err
		}
		if !c.Enabled || len(c.Actions) == 0 || r.ours(c) || c.Actions[0].Type == "drop" || r.o.ReplaceCatchAll {
			continue
		}
		does := c.Actions[0].Type
		if v := c.Actions[0].Value; len(v) > 0 {
			does += " to " + clip(strings.Join(v, ", "), 120)
		}
		return "", &CatchAllError{Domain: z.Name, Does: does}
	}
	return "free to use", nil
}

// ours reports whether a catch-all already goes to this HuntMail.
func (r *run) ours(c catchAll) bool {
	return c.Enabled && len(c.Actions) == 1 && c.Actions[0].Type == "worker" && slices.Equal(c.Actions[0].Value, []string{r.o.Name})
}

func (r *run) script() string { return "/accounts/" + r.account + "/workers/scripts/" + r.o.Name }

type binding struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Text       string `json:"text,omitempty"`
	ID         string `json:"id,omitempty"`
	DatabaseID string `json:"database_id,omitempty"`
}

func (r *run) findWorker(ctx context.Context) (string, error) {
	r.vars = map[string]string{}
	var settings struct {
		Bindings []binding `json:"bindings"`
	}
	err := r.cf.call(ctx, http.MethodGet, r.script()+"/settings", nil, &settings, "look for the "+r.o.Name+" Worker", "Workers Scripts Edit")
	switch {
	case notFound(err):
		r.newKey = true
		return "none yet: it will be set up", nil
	case err != nil:
		return "", err
	}
	r.exists = true
	for _, b := range settings.Bindings {
		switch {
		case b.Type == "plain_text":
			r.vars[b.Name] = b.Text
		case b.Type == "d1" && b.Name == r.b.D1Binding:
			r.dbID = cmp(b.DatabaseID, b.ID)
		}
	}
	var secrets []struct {
		Name string `json:"name"`
	}
	if err := r.cf.call(ctx, http.MethodGet, r.script()+"/secrets", nil, &secrets, "list HuntMail's secrets", "Workers Scripts Edit"); err != nil {
		return "", err
	}
	for _, s := range secrets {
		r.secrets = append(r.secrets, s.Name)
	}
	has := slices.Contains(r.secrets, "API_KEY") && slices.Contains(r.secrets, "BASE_PATH")
	switch {
	case !has || r.o.NewKey:
		r.newKey = true
		return "found: it will be updated, with a new key", nil
	case r.knows():
		return "found: it will be updated, keeping its key", nil
	default:
		return "", ErrKeyUnknown
	}
}

// knows reports whether HuntDeck's HuntMail address is this Worker's, and
// takes its base path from it.
func (r *run) knows() bool {
	u, err := url.Parse(strings.TrimRight(r.o.KnownURL, "/"))
	if err != nil || u.Scheme != "https" || !strings.HasPrefix(u.Host, r.o.Name+".") || !strings.HasSuffix(u.Host, ".workers.dev") {
		return false
	}
	base := strings.TrimPrefix(u.Path, "/")
	if base == "" || strings.Contains(base, "/") {
		return false
	}
	r.base = base
	return true
}

func (r *run) findSubdomain(ctx context.Context) (string, error) {
	var s struct {
		Subdomain string `json:"subdomain"`
	}
	err := r.cf.call(ctx, http.MethodGet, "/accounts/"+r.account+"/workers/subdomain", nil, &s, "read the workers.dev subdomain", "Account Settings Read")
	if err != nil && !notFound(err) {
		return "", err
	}
	if s.Subdomain == "" {
		return "", errors.New("this Cloudflare account has no workers.dev subdomain yet: open Workers & Pages in Cloudflare's dashboard once to choose it, then run this again")
	}
	r.sub = s.Subdomain
	if !r.newKey && r.exists {
		// The key kept is HuntDeck's: its address must be this account's HuntMail.
		if u, err := url.Parse(r.o.KnownURL); err != nil || "https://"+u.Host != r.origin() {
			return "", ErrKeyUnknown
		}
	}
	return r.origin(), nil
}

func (r *run) ensureDatabase(ctx context.Context) (string, error) {
	if r.dbID != "" {
		return "kept", nil
	}
	path := "/accounts/" + r.account + "/d1/database"
	find := func() (string, error) {
		var dbs []struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}
		if err := r.cf.call(ctx, http.MethodGet, path+"?name="+url.QueryEscape(r.o.Name), nil, &dbs, "list the databases", "D1 Edit"); err != nil {
			return "", err
		}
		for _, db := range dbs {
			if db.Name == r.o.Name {
				return db.UUID, nil
			}
		}
		return "", nil
	}
	id, err := find()
	if err != nil {
		return "", err
	}
	if id != "" {
		r.dbID = id
		return "found", nil
	}
	var made struct {
		UUID string `json:"uuid"`
	}
	if err := r.cf.call(ctx, http.MethodPost, path, map[string]string{"name": r.o.Name}, &made, "create the database", "D1 Edit"); err != nil {
		// A retry after a lost answer finds the one it made.
		if id, ferr := find(); ferr == nil && id != "" {
			r.dbID = id
			return "created", nil
		}
		return "", err
	}
	if made.UUID == "" {
		return "", errors.New("the database was created, but Cloudflare didn't say its id: run this again")
	}
	r.dbID = made.UUID
	return "created", nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
