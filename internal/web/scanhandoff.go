package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	}
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

// handOff creates one scan task per batch of waiting targets. It is what both
// the button and the periodic pass call, so they cannot drift.
func (s *server) handOff(ctx context.Context, manual bool) (int, error) {
	cfg, err := s.Store.ScanConfigRow(ctx)
	if err != nil {
		return 0, err
	}
	if !manual && !cfg.Enabled {
		return 0, nil
	}
	if cfg.TemplateID == "" || cfg.NodeName == "" {
		return 0, fmt.Errorf("pick a template and a node first")
	}
	if s.ScanKey.Reveal() == "" {
		return 0, fmt.Errorf("no scanner key configured")
	}
	ctx, cancel := context.WithTimeout(ctx, scanTaskTimeout)
	defer cancel()
	client := s.scanClient()
	cands, err := s.Store.ScanCandidates(ctx, cfg.Kinds, cfg.PerTask*4)
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}
	perTask := cfg.PerTask
	if perTask <= 0 {
		perTask = 50
	}
	created := 0
	for start := 0; start < len(cands); start += perTask {
		end := start + perTask
		if end > len(cands) {
			end = len(cands)
		}
		batch := cands[start:end]
		targets := make([]string, 0, len(batch))
		for _, c := range batch {
			targets = append(targets, c.Target)
		}
		name := "huntdeck-" + s.Store.Now().UTC().Format("20060102-150405") + "-" + strconv.Itoa(created+1)
		id, err := client.CreateTask(ctx, name, cfg.TemplateID, cfg.NodeName, targets)
		if err != nil {
			return created, err
		}
		if err := client.StartTask(ctx, id); err != nil {
			// Created but not started: record it anyway, so it is not created
			// twice; starting is a separate act the operator can do.
			s.Log.WarnContext(ctx, "scan: task created but not started", "task", id, "error", err)
		}
		if err := s.Store.MarkHandedOff(ctx, batch, id); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
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
	Error       string
}
