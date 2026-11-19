package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	d.Waiting, _ = s.Store.ScanCandidates(r.Context(), d.Config.Kinds, 200)
	d.HandedOff, d.LastHandoff, _ = s.Store.ScanHandoffCount(r.Context())
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
	created, err := s.handOff(r.Context(), true)
	if err != nil {
		s.Log.WarnContext(r.Context(), "scan: handoff failed", "error", err)
		http.Redirect(w, r, "/scan?error="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/scan?created="+strconv.Itoa(created), http.StatusSeeOther)
}

// handOff runs the shared handoff: the page and the poll loop use one
// implementation, so they cannot drift.
func (s *server) handOff(ctx context.Context, manual bool) (int, error) {
	if s.ScanKey.Reveal() == "" {
		return 0, fmt.Errorf("no scanner key configured")
	}
	ctx, cancel := context.WithTimeout(ctx, scanTaskTimeout)
	defer cancel()
	return scanhandoff.Run(ctx, s.Store, s.scanClient(), manual)
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
}
