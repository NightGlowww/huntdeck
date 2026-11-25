package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/jev"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/scanhandoff"
	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The handoff: targets HuntDeck finds, scanned by a ScopeSentry instance over
// its MCP endpoint. Off unless the operator turns it on and picks a template
// and a node — a scanner that starts scanning by itself is not a default.
const scanTaskTimeout = 30 * time.Second

func (s *server) scanClient() *scopesentry.Client {
	return &scopesentry.Client{BaseURL: s.ScanURL, Key: s.ScanKey.Reveal()}
}

// scanPage shows the handoff: whether the scanner answers, what it offers, and
// how many targets are waiting.
func (s *server) scanPage(w http.ResponseWriter, r *http.Request) {
	d := scanPageData{}
	cfg, err := s.Store.ScanConfigRow(r.Context())
	if err == nil {
		d.Config = cfg
	}
	if s.ScanKey.Reveal() == "" {
		d.Off = true
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		client := s.scanClient()
		if tpl, err := client.Templates(ctx); err != nil {
			d.Error = err.Error()
		} else {
			d.Templates = tpl
		}
		if nodes, err := client.Nodes(ctx); err == nil {
			d.Nodes = nodes
		}
		// The same number the handoff checks before creating anything: shown
		// so a refusal is expected rather than a surprise.
		if pct, ok, err := client.MemAvailablePercent(ctx); err == nil && ok {
			d.ScannerFree = pct
			d.ScannerFreeKnown = true
			d.BelowFloor = pct < scanhandoff.MinFreePercent
		}
	}
	// The three POSTs redirect back here with what happened; without reading
	// their query a failed handoff looked like a page that did nothing at all.
	q := r.URL.Query()
	d.Saved = q.Get("saved") != ""
	d.Created = q.Get("created")
	d.Failure = q.Get("error")
	d.Busy = q.Get("busy")
	// A failed read is not an empty queue: the page used to show "nothing
	// waiting" over a database error, which reads as a quiet day.
	if d.Waiting, err = s.Store.ScanCandidates(r.Context(), d.Config.Kinds, 400); err != nil {
		d.WaitingError = "could not read the candidates: " + err.Error()
	} else {
		// A host in two programs is one row to a reader, as it is one task to the
		// handoff: the same name twice reads as a bug in the list.
		d.Waiting = firstPerTarget(d.Waiting)
	}
	if d.HandedOff, d.LastHandoff, err = s.Store.ScanHandoffCount(r.Context()); err != nil && d.WaitingError == "" {
		d.WaitingError = "could not read the handoff count: " + err.Error()
	}
	// A backlog that has not moved for hours is the state that took an evening to
	// notice by hand: say it on the page, with the scanner's own reason if the
	// last attempt gave one.
	if stalled := scanhandoff.StalledFor(d.LastHandoff, s.now(r.Context()), len(d.Waiting)); stalled > 0 {
		d.WaitingSince = humanize.Duration(stalled)
	}
	// What went out, and under which task id: the scanner names a task however it
	// likes, so this is how a task in its list is traced back to a program.
	if tasks, terr := s.Store.HandoffTasks(r.Context(), 40); terr == nil {
		d.RecentState = make(map[string]store.HandoffTask, len(tasks))
		for _, t := range tasks {
			d.RecentState[t.TaskID] = t
		}
	}
	if d.Recent, d.RecentOrder, err = s.Store.RecentHandoffs(r.Context(), 8); err != nil && d.WaitingError == "" {
		d.WaitingError = "could not read the recent handoffs: " + err.Error()
	}
	s.render(w, r, http.StatusOK, "scan", pageData{Title: "Scan handoff", Nav: "scan", Data: d})
}

// scanSave stores the choices (and mints nothing): template, node, size, kinds.
func (s *server) scanSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	perTask, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("per_task")))
	var kinds []string
	for _, k := range strings.Split(r.PostFormValue("kinds"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds = append(kinds, k)
		}
	}
	cfg := store.ScanConfig{
		Enabled:    r.PostFormValue("enabled") != "",
		TemplateID: strings.TrimSpace(r.PostFormValue("template_id")),
		NodeName:   strings.TrimSpace(r.PostFormValue("node_name")),
		PerTask:    perTask,
		Kinds:      kinds,
	}
	if err := s.Store.SaveScanConfig(r.Context(), cfg); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/scan?saved=1", http.StatusSeeOther)
}

// scanNow hands the waiting targets over, once, on demand.
func (s *server) scanNow(w http.ResponseWriter, r *http.Request) {
	sum, err := s.handOff(r.Context(), true)
	notify.ReportHandoff(r.Context(), s.Store, s.Log, sum, err)
	if err != nil {
		// A queued scanner is capacity, not a fault: the page says "wait",
		// in the warning colour, and does not call it a failure.
		if errors.Is(err, scanhandoff.ErrBusy) {
			s.Log.InfoContext(r.Context(), "scan: scanner busy", "error", err)
			http.Redirect(w, r, "/scan?busy="+urlQuery(err.Error()), http.StatusSeeOther)
			return
		}
		s.Log.WarnContext(r.Context(), "scan: handoff failed", "error", err)
		http.Redirect(w, r, "/scan?error="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/scan?created="+strconv.Itoa(sum.Tasks), http.StatusSeeOther)
}

// handOff runs the shared handoff: the page and the poll loop use one
// implementation, so they cannot drift.
func (s *server) handOff(ctx context.Context, manual bool) (scanhandoff.Summary, error) {
	if s.ScanKey.Reveal() == "" {
		return scanhandoff.Summary{}, fmt.Errorf("no scanner key configured")
	}
	ctx, cancel := context.WithTimeout(ctx, scanTaskTimeout)
	defer cancel()
	var opt scanhandoff.Options
	if s.JevKey.Reveal() != "" {
		opt.Opinion = scanhandoff.NewJevOpinion(&jev.Client{BaseURL: s.JevURL, Key: s.JevKey.Reveal(), Model: s.JevModel, Proxy: s.JevProxy, Provider: s.JevProvider, AccountID: s.JevAccountID, WorkspaceID: s.JevWorkspaceID})
	}
	return scanhandoff.RunWith(ctx, s.Store, s.scanClient(), manual, opt)
}

func urlQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&", "%26"), "#", "%23")
}

type scanPageData struct {
	Config      store.ScanConfig
	Templates   []scopesentry.Template
	Nodes       []scopesentry.Node
	Waiting     []store.ScanCandidate
	HandedOff   int
	LastHandoff time.Time
	Off         bool
	// ScannerFree is the scanner host's free memory, and BelowFloor
	// says the handoff would be refused right now.
	ScannerFree      float64
	ScannerFreeKnown bool
	BelowFloor       bool
	Error            string
	// Saved, Created and Failure are what the POSTs report when they come back.
	Saved   bool
	Created string
	Failure string
	// WaitingSince is how long the oldest candidate has been waiting, and why
	// nothing moved: a backlog with no handoff for hours is the state that took
	// an evening to notice by hand.
	WaitingSince string
	// Recent are the tasks handed over lately, by the id the scanner gave back.
	Recent      map[string]store.HandoffBatch
	RecentOrder []string
	// RecentState is what the scanner said about each of them last, by task id:
	// the pipeline's last link, shown where the handoffs are.
	RecentState map[string]store.HandoffTask
	// WaitingError is this side failing to read its own tables: shown whether or
	// not a scanner is configured, because it says nothing about the scanner.
	WaitingError string
	// Busy is the scanner refusing for capacity: nothing failed, it is full.
	Busy string
}

// firstPerTarget keeps one candidate per target, first appearance first: the
// handoff does the same, and the list should not disagree with it.
func firstPerTarget(cands []store.ScanCandidate) []store.ScanCandidate {
	seen := make(map[string]bool, len(cands))
	out := cands[:0]
	for _, c := range cands {
		if seen[c.Target] {
			continue
		}
		seen[c.Target] = true
		out = append(out, c)
	}
	return out
}
