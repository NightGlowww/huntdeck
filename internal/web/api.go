package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiErrorJSON{Error: msg})
}

func (s *server) apiServerError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("API request failed", "path", r.URL.Path, "err", err)
	writeJSONError(w, http.StatusInternalServerError, "internal error; see server log")
}

func (s *server) apiStatus(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Store.Summary(r.Context())
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	runs, err := s.Store.LatestRuns(r.Context())
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	checked, err := s.Store.PlatformsChecked(r.Context())
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiStatusJSON{Version: s.Version, ReadOnly: s.ReadOnly, Platforms: []apiStatusPlatform{}, LastPolls: []apiStatusRun{}}
	for _, p := range sum {
		row := apiStatusPlatform{p.Platform, p.Programs, p.Private, p.Open, p.Bounty, p.Removed,
			p.InScopeEntries, p.OutScopeEntries, p.Targets, nil}
		if i := slices.IndexFunc(checked, func(c store.PlatformChecked) bool { return c.Platform == p.Platform }); i >= 0 {
			row.CheckedAt = &checked[i].At
		}
		out.Platforms = append(out.Platforms, row)
	}
	for _, r := range runs {
		row := apiStatusRun{Platform: r.Platform, Mode: r.Mode, Status: r.Status, StartedAt: r.StartedAt,
			FinishedAt: r.FinishedAt, ProgramsSeen: r.ProgramsSeen, Changes: r.Changes, Requests: r.Requests,
			Error: r.Error}
		// A running poll's stored counters are still zero - they are written when
		// it ends - so ask the syncer what it is doing instead of reporting nothing.
		if r.Status == store.RunRunning && s.Progress != nil {
			if pr, ok := s.Progress(r.ID); ok {
				row.Progress = &apiRunProgress{Step: pr.Step, Done: pr.Done, Total: pr.Total,
					Requests: pr.Requests, IdleSeconds: int(time.Since(pr.LastActivity).Seconds())}
			}
		}
		out.LastPolls = append(out.LastPolls, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// apiTargets defaults to plain text, one value per line, for piping; the
// whole list is streamed (from offset, up to limit if given; a read-only
// copy gives a page at a time, see cappedTargets). format=json returns one page of rows with their
// programs (limit, default 1000, max 5000; offset) and the total.
func (s *server) apiTargets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := targetFilterFrom(q, s.now(r.Context()))
	since, err := sinceParam(q)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.ChangedSince = since
	if f.Kind == "" && len(f.AssetTypes) == 0 && !strings.EqualFold(q.Get("kind"), kindAll) {
		writeJSONError(w, http.StatusBadRequest, "kind is required: wildcards, domains, urls, ips, cidrs, roots, all, or an asset type such as apps, android, ios or hardware")
		return
	}
	best := q.Get("sort") == "best"
	if q.Get("format") == "json" {
		limit := clampInt(q.Get("limit"), 1000, 1, 5000)
		offset := clampInt(q.Get("offset"), 0, 0, 1<<30)
		list := s.Store.TargetRows
		if best {
			list = s.Store.BestTargets
		}
		rows, total, err := list(r.Context(), f, q.Get("q"), limit, offset)
		if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		out := apiTargetListJSON{Total: total, Limit: limit, Offset: offset, Targets: []apiTargetRow{}}
		for _, t := range rows {
			row := apiTargetRow{t.Kind, t.Value, t.InScope, t.Bounty, t.AddedAt, refJSON(t.Program), nil, t.Reasons, t.Joined}
			if best {
				row.Score = &t.Score
			}
			out.Targets = append(out.Targets, row)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if q.Get("download") != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+f.Kind+`s.txt"`)
	}
	each := s.Store.EachTarget
	if best {
		each = s.Store.EachBestTarget
	}
	f.Offset = clampInt(q.Get("offset"), 0, 0, 1<<30)
	f.Query = q.Get("q")
	comment := textComment(w, r)
	if s.ReadOnly {
		s.cappedTargets(w, r, f, comment, each)
		return
	}
	f.Limit = clampInt(q.Get("limit"), 0, 0, 1<<30)
	bw := bufio.NewWriterSize(w, 32<<10)
	_, _ = bw.WriteString(comment) // buffered: the status isn't out yet
	started := false
	err = each(r.Context(), f, func(v string) error {
		started = true
		_, err := bw.WriteString(v + "\n")
		return err
	})
	switch {
	case err == nil:
		_ = bw.Flush()
	case !started:
		s.apiServerError(w, r, err)
	case r.Context().Err() == nil:
		// The status line has gone out once streaming starts; the client sees a cut-off body.
		s.Log.Warn("targets export stopped", "err", err)
	}
}

// readOnlyTargetLines is the most a read-only copy's plain-text targets
// export gives at once: anyone may ask it, and the whole list (tens of
// thousands of domains) is for the user's own copy.
const readOnlyTargetLines = 5000

// cappedTargets is a read-only copy's plain-text targets export: one page
// of at most readOnlyTargetLines (limit, offset), gathered before sending
// so that, when there are more, the headers can say where the next page is
// (Link rel="next") without a line in the list itself that a pipe would
// take for a target.
func (s *server) cappedTargets(w http.ResponseWriter, r *http.Request, f store.TargetFilter, comment string,
	each func(context.Context, store.TargetFilter, func(string) error) error) {
	limit := clampInt(r.URL.Query().Get("limit"), readOnlyTargetLines, 1, readOnlyTargetLines)
	f.Limit = limit + 1 // one more says whether there's a next page
	var b strings.Builder
	n := 0
	err := each(r.Context(), f, func(v string) error {
		if n++; n <= limit {
			b.WriteString(v + "\n")
		}
		return nil
	})
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	w.Header().Set("X-Targets-Limit", strconv.Itoa(limit))
	if n > limit {
		next := r.URL.Query()
		next.Set("offset", strconv.Itoa(f.Offset+limit))
		w.Header().Set("Link", `</api/v1/targets?`+next.Encode()+`>; rel="next"`)
	}
	_, _ = io.WriteString(w, comment+b.String())
}

func (s *server) apiChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ChangeFilter{
		Platform:   canonicalPlatform(q.Get("platform")),
		BountyOnly: isTrue(q.Get("bounty")),
		NoBounty:   q.Get("bounty") == "vdp",
		Limit:      clampInt(q.Get("limit"), 200, 1, 1000),
		Offset:     clampInt(q.Get("offset"), 0, 0, 1<<30),
		Newest:     q.Get("order") == "newest",
	}
	since, err := parseSince(q.Get("since"), 24*time.Hour)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Since = since
	if k := q.Get("kind"); k != "" {
		f.Kinds = strings.Split(k, ",")
	}
	if prog := q.Get("program"); prog != "" {
		plat, handle, ok := strings.Cut(prog, "/")
		ids, err := s.Store.ProgramIDs(r.Context(), canonicalPlatform(plat))
		if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		if f.ProgramID = ids[handle]; !ok || f.ProgramID == 0 {
			writeJSONError(w, http.StatusNotFound, "no such program")
			return
		}
	}
	list, total, err := s.Store.Changes(r.Context(), f)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiChangeListJSON{Total: total, Changes: []apiChange{}}
	for _, c := range list {
		out.Changes = append(out.Changes, apiChange{c.ID, c.OccurredAt, c.Kind, c.Field, c.OldValue, c.NewValue,
			describeChange(c), refJSON(c.Program)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPoll(w http.ResponseWriter, r *http.Request) {
	switch res := s.startPoll(r.PathValue("platform"), r.URL.Query().Get("mode")); res {
	case "msg=poll-started":
		writeJSON(w, http.StatusAccepted, apiOKJSON{Status: "started"})
	case "err=poll-running":
		writeJSONError(w, http.StatusConflict, "a poll of this platform is already running")
	case "err=poll-unknown":
		writeJSONError(w, http.StatusNotFound, "platform not configured")
	case "err=poll-off":
		writeJSONError(w, http.StatusConflict, "platform disabled in Settings")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "polling unavailable")
	}
}

// parseSince accepts an RFC 3339 time or a duration like "24h" or "7d".
func parseSince(v string, def time.Duration) (time.Time, error) {
	if v == "" {
		return time.Now().Add(-def), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			return time.Now().Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return time.Now().Add(-d), nil
	}
	return time.Time{}, errors.New("since must be RFC 3339 (2026-01-02T15:04:05Z) or a duration like 24h or 7d")
}

func clampInt(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

// nonNil makes an empty list encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
