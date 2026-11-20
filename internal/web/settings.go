package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

type settingsData struct {
	Tab        string // the open tab's key
	Tabs       []settingsTab
	Platforms  []platformRow
	LatestRuns []store.Run
	Runs       []store.Run
	Live       map[int64]liveRun // the running ones' progress, by run ID
	MoreRuns   string            // a link to more poll history, if there is more
	HuntPreset string            // the chosen HuntScore preset's key
	Zone       zoneForm
	Browser    *browserData  // the Browser tab, if there's a login browser
	Accounts   *accountsData // the Accounts tab, where settings can be saved
	CanSave    bool          // there's an Accounts tab to set credentials in
	DemoSeed   *demoSeedData // the Polling tab's demo seed, where it can be exported
	// AI is the optional model: whether it is wired, and how many policy
	// readings it has cached. Off is normal; saying so is not.
	AI aiState
}

// settingsTab is a tab of the Settings page (?tab=).
type settingsTab struct {
	Key, Label string
	Count      int // shown after the label when not 0
	On         bool
}

// settingsTabs are the tabs' keys and labels, the first the default.
var settingsTabs = []settingsTab{{Key: "polling", Label: "Polling"}, {Key: "accounts", Label: "Accounts"}, {Key: "browser", Label: "Browser"},
	{Key: "you", Label: "HuntScore"}, {Key: "display", Label: "Display"}}

// URL is the tab's address.
func (t settingsTab) URL() string {
	if t.Key == settingsTabs[0].Key {
		return "/settings"
	}
	return "/settings?tab=" + t.Key
}

// openTab is the tab a request shows: the one its form is on, for a form
// shown again with an error, else ?tab=.
func openTab(r *http.Request) string {
	switch r.URL.Path {
	case "/settings/huntscore":
		return "you"
	case "/settings/timezone":
		return "display"
	case "/settings/accounts", "/settings/accounts/reset", "/settings/restart":
		return "accounts"
	}
	keys := make([]string, len(settingsTabs))
	for i, t := range settingsTabs {
		keys[i] = t.Key
	}
	return oneOf(r.URL.Query().Get("tab"), keys...)
}

// Poll history shows the latest few polls, or more on request.
const (
	runsShown = 12
	runsMore  = 60
)

// platformRow is one platform's polling settings on the settings page.
type platformRow struct {
	Name string
	scheduler.Settings
	Default    scheduler.Settings
	Overridden bool
	SignIn     []signIn    // each credential and how it did
	Web        *webSyncRow // its browser sync, if it has one: a row under it
	// NextQuick and NextFull say when the next poll is due, as "in 12m".
	// The scheduler keeps the real time; this is the same arithmetic it does,
	// from the last run and the interval, and the page says "about" because of
	// that.
	NextQuick, NextFull    string
	CapsRemaining, CapsCap int
	// CapsRemaining and CapsCap are the Bugcrowd submission allowance; a zero cap
	// means there is nothing to show. The template composes the sentence, so the
	// words stay translatable.
}

// platformSettings returns a platform's current settings; without a
// Schedule (CLI tools, tests) every platform is enabled with polls off.
func (s *server) platformSettings(name string) scheduler.Settings {
	if s.Schedule == nil {
		return scheduler.Settings{Enabled: true}
	}
	return s.Schedule.Get(name)
}

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("tab") == "api" { // the API has its own page now
		http.Redirect(w, r, "/api", http.StatusMovedPermanently)
		return
	}
	s.renderSettings(w, r, http.StatusOK, "")
}

func (s *server) renderSettings(w http.ResponseWriter, r *http.Request, status int, flashErr string) {
	d := settingsData{Tab: openTab(r)}
	d.CanSave = s.Accounts != nil
	d.DemoSeed = s.demoSeedData(r.Context())
	d.AI = s.aiStateFor(r.Context())
	hidden := map[string]bool{"browser": !s.hasBrowser(), "accounts": !d.CanSave}
	if s.ReadOnly {
		hidden = nil // a tour: every tab, with nothing in it
	}
	if hidden[d.Tab] {
		d.Tab = settingsTabs[0].Key
	}
	for _, t := range settingsTabs {
		if hidden[t.Key] {
			continue
		}
		t.On = t.Key == d.Tab
		if t.Key == "polling" {
			t.Count = len(s.Platforms)
		}
		d.Tabs = append(d.Tabs, t)
	}
	var err error
	d.LatestRuns, err = s.Store.LatestRuns(r.Context())
	webs := s.webSyncRows(d.LatestRuns)
	for _, p := range s.Platforms {
		row := platformRow{Name: p.Name, Settings: s.platformSettings(p.Name)}
		if s.Schedule != nil {
			row.Default, row.Overridden = s.Schedule.Default(p.Name), s.Schedule.Overridden(p.Name)
		}
		if i := slices.IndexFunc(webs, func(w webSyncRow) bool { return w.Name == p.Name }); i >= 0 {
			row.Web = &webs[i]
		}
		row.SignIn = s.signIns(p, d.LatestRuns, row.Web)
		d.Platforms = append(d.Platforms, row)
	}
	if d.Tab == "browser" {
		d.Browser = s.browserTab(d.Platforms)
	}
	if err == nil && d.Tab == "accounts" {
		d.Accounts, err = s.accountsTab(r.Context(), d.Platforms)
	}
	if err == nil && d.Tab == "polling" {
		limit := runsShown
		if r.URL.Query().Get("runs") == "more" {
			limit = runsMore
		}
		d.Runs, err = s.Store.RecentRuns(r.Context(), limit+1)
		if len(d.Runs) > limit {
			d.Runs = d.Runs[:limit]
			if limit == runsShown {
				d.MoreRuns = "/settings?runs=more#history"
			}
		}
		d.Live = s.liveRuns(r.Context(), d.Runs)
		s.fillNextDue(r.Context(), d.Platforms)
		s.fillBugcrowdCaps(r.Context(), d.Platforms)
	}
	if err == nil {
		var set map[string]string
		set, err = s.Store.Settings(r.Context(), huntscore.PresetSetting)
		d.HuntPreset = huntscore.FindPreset(set[huntscore.PresetSetting]).Key
	}
	if err == nil {
		d.Zone, err = s.zoneForm(r.Context())
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "settings", pageData{Title: "Settings", Nav: "settings", Data: d, FlashErr: flashErr})
}

// saveHuntPreset stores the HuntScore weighting and rescores every program
// with it.
func (s *server) saveHuntPreset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	key := r.PostFormValue("preset")
	if huntscore.FindPreset(key).Key != key {
		s.renderSettings(w, r, http.StatusBadRequest, "Unknown HuntScore weighting.")
		return
	}
	err := s.Store.Write(r.Context(), func(tx *store.Tx) error { return tx.PutSetting(r.Context(), huntscore.PresetSetting, key) })
	if err == nil {
		err = huntscore.Refresh(r.Context(), s.Store, s.Store.Now())
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?tab=you&msg=huntscore-saved", http.StatusSeeOther)
}

// saveSchedule saves platforms' enabled flags and poll intervals: each
// platform field's, from enabled_<name>, quick_<name> and full_<name>, or,
// for a single platform, from enabled, quick and full. Nothing is saved
// unless every platform's settings are valid, and unchanged ones aren't
// saved (so they keep following .env).
func (s *server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if s.Schedule == nil {
		s.renderSettings(w, r, http.StatusServiceUnavailable, "Settings can't be changed in this process.")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	names := r.PostForm["platform"]
	field := func(key, name string) string {
		if v, ok := r.PostForm[key+"_"+name]; ok {
			return v[0]
		}
		if len(names) == 1 {
			return r.PostFormValue(key)
		}
		return ""
	}
	sets := map[string]scheduler.Settings{}
	for _, raw := range names {
		name := canonicalPlatform(raw)
		if !slices.Contains(s.platformNames(), name) {
			s.renderSettings(w, r, http.StatusNotFound, "That platform isn't configured.")
			return
		}
		set := scheduler.Settings{Enabled: isTrue(field("enabled", raw))}
		var err error
		if set.Quick, err = scheduler.ParseInterval(field("quick", raw)); err == nil {
			if set.Full, err = scheduler.ParseInterval(field("full", raw)); err == nil {
				err = set.Validate()
			}
		}
		var invalid scheduler.ValidationError
		if errors.As(err, &invalid) {
			s.renderSettings(w, r, http.StatusBadRequest, platformLabel(name)+": "+invalid.Error()+".")
			return
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
		sets[name] = set
	}
	// A browser sync's switch and interval sit in its platform's row, saved
	// with the rest; the scheduler validates the interval as it saves.
	webs := map[string]store.WebSyncSettings{}
	for _, raw := range r.PostForm["web"] {
		name := canonicalPlatform(raw)
		if _, ok := s.WebSyncs[name]; !ok {
			s.renderSettings(w, r, http.StatusNotFound, "That platform has no browser sync set up.")
			return
		}
		ws, err := s.webSyncFrom(name, r.PostFormValue("web_enabled_"+raw), r.PostFormValue("web_interval_"+raw))
		if err != nil {
			s.renderSettings(w, r, http.StatusBadRequest, platformLabel(name)+" browser sync: "+err.Error()+".")
			return
		}
		webs[name] = ws
	}
	for name, ws := range webs {
		if ws == s.Schedule.Web(name) {
			continue
		}
		var invalid scheduler.ValidationError
		if err := s.Schedule.SetWeb(r.Context(), name, ws); errors.As(err, &invalid) {
			s.renderSettings(w, r, http.StatusBadRequest, platformLabel(name)+" browser sync: "+invalid.Error()+".")
			return
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	for name, set := range sets {
		if set == s.Schedule.Get(name) {
			continue
		}
		if err := s.Schedule.Set(r.Context(), name, set); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/settings?msg=settings-saved", http.StatusSeeOther)
}

// resetSchedule drops a platform's saved settings.
func (s *server) resetSchedule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if s.Schedule == nil {
		s.renderSettings(w, r, http.StatusServiceUnavailable, "Settings can't be changed in this process.")
		return
	}
	err := s.Schedule.Reset(r.Context(), canonicalPlatform(r.PostFormValue("platform")))
	switch {
	case errors.Is(err, scheduler.ErrUnknownPlatform):
		s.renderSettings(w, r, http.StatusNotFound, "That platform isn't configured.")
	case err != nil:
		s.serverError(w, r, err)
	default:
		http.Redirect(w, r, "/settings?msg=settings-reset", http.StatusSeeOther)
	}
}

// baseURL is the scheme and host the request came in on. hostGuard has
// already vetted the host; the result is only displayed.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) pollNow(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	http.Redirect(w, r, "/settings?"+s.startPoll(r.PostFormValue("platform"), r.PostFormValue("mode")), http.StatusSeeOther)
}

// startPoll returns the flash query for the outcome.
func (s *server) startPoll(platformName, mode string) string {
	if s.Poller == nil {
		return "err=poll-disabled"
	}
	mode = oneOf(mode, "quick", "full", "web")
	err := s.Poller.PollNow(canonicalPlatform(platformName), mode)
	switch {
	case errors.Is(err, store.ErrRunInProgress):
		return "err=poll-running"
	case errors.Is(err, ErrUnknownPlatform):
		return "err=poll-unknown"
	case errors.Is(err, ErrPlatformDisabled):
		return "err=poll-off"
	case err != nil:
		s.Log.Error("start poll", "err", err)
		return "err=poll-unknown"
	}
	return "msg=poll-started"
}

// fillNextDue puts "in 12m" under each interval, from the last run and the
// interval the scheduler will use. It is the same arithmetic the scheduler
// does, not the scheduler's own timer: a manual run or a busy retry moves the
// real one, which is why the page says "about".
func (s *server) fillNextDue(ctx context.Context, rows []platformRow) {
	if s.Schedule == nil {
		return
	}
	last, err := s.Store.LatestRuns(ctx)
	if err != nil {
		return
	}
	started := map[string]time.Time{}
	for _, r := range last {
		started[r.Platform+"|"+r.Mode] = r.StartedAt
	}
	now := time.Now()
	for i := range rows {
		name := rows[i].Name
		if rows[i].Enabled {
			rows[i].NextQuick = dueIn(started[name+"|"+syncer.ModeQuick], rows[i].Interval(syncer.ModeQuick), now)
			rows[i].NextFull = dueIn(started[name+"|"+syncer.ModeFull], rows[i].Interval(syncer.ModeFull), now)
		}
	}
}

// dueIn is how long until the next poll, given when the last one started and
// the interval; empty when the interval is off or there is no last run to
// count from.
func dueIn(lastStart time.Time, interval time.Duration, now time.Time) string {
	if interval <= 0 || lastStart.IsZero() {
		return ""
	}
	d := lastStart.Add(interval).Sub(now)
	if d <= 0 {
		return "now"
	}
	return humanize.Duration(d)
}

// fillBugcrowdCaps puts the account's submission allowance on its row. One
// request, and only when the process has a client to make it with; a failure
// leaves the line out rather than showing a zero, which would read as "none
// left".
func (s *server) fillBugcrowdCaps(ctx context.Context, rows []platformRow) {
	if s.BugcrowdCaps == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	caps, ok := s.BugcrowdCaps(ctx)
	if !ok {
		return
	}
	for i := range rows {
		if rows[i].Name != "bugcrowd" {
			continue
		}
		rows[i].CapsRemaining, rows[i].CapsCap = caps.WeeklyRemaining, caps.WeeklyCap
	}
}
