package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/ai"
	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/overrides"
)

// The optional model has its own page: an endpoint, a model name and a key are
// configuration of their own, not a platform credential, and the thing they
// turn on (reading each program's policy once) deserves to be visible.
func (s *server) llmPage(w http.ResponseWriter, r *http.Request) {
	d := llmPageData{}
	if s.Accounts == nil {
		for _, f := range config.Editable {
			if f.Group == config.GroupAI {
				d.Fields = append(d.Fields, overrides.FieldState{Field: f, From: overrides.FromNone})
			}
		}
	} else if fields, err := s.Accounts.Fields(r.Context()); err == nil {
		for _, f := range fields {
			if f.Group == config.GroupAI {
				d.Fields = append(d.Fields, f)
			}
		}
	}
	d.Configured = s.AI != nil
	d.Model = s.AIModel
	d.Endpoint = s.AIBaseURL
	if v := r.URL.Query().Get("checked"); v == "1" {
		d.Checked = true
		d.CheckAnswer = r.URL.Query().Get("answer")
		d.CheckMS = r.URL.Query().Get("ms")
	} else if v := r.URL.Query().Get("failed"); v != "" {
		d.Checked = true
		d.CheckError = v
	}
	s.render(w, r, http.StatusOK, "llm", pageData{Title: "Optional model", Nav: "llm", Data: d})
}

// llmTest asks the configured model one question, the same way the ai-check
// command does, so "is it working" is answerable from the page.
func (s *server) llmTest(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil {
		http.Redirect(w, r, "/llm?failed="+urlQuery("no endpoint configured: set the endpoint and the model above"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var out struct {
		Answer string `json:"answer"`
	}
	start := time.Now()
	err := s.AI.CompleteJSON(ctx, "You answer with JSON only.",
		`Reply with {"answer":"ok"} and nothing else.`, &out)
	if err != nil {
		http.Redirect(w, r, "/llm?failed="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	took := time.Since(start).Round(time.Millisecond)
	http.Redirect(w, r, "/llm?checked=1&answer="+urlQuery(out.Answer)+"&ms="+urlQuery(took.String()), http.StatusSeeOther)
}

type llmPageData struct {
	Fields      []overrides.FieldState
	Configured  bool
	Model       string
	Endpoint    string
	Checked     bool
	CheckAnswer string
	CheckMS     string
	CheckError  string
}

// keep the ai import honest: the page reports what the client is
var _ = ai.Client{}
var _ = strings.TrimSpace
