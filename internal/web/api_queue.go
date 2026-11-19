package web

import (
	"net/http"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntqueue"
	"github.com/mdpsec/huntdeck/internal/store"
)

// apiQueue is HuntQueue as data: the targets ranked by "can I start today, and
// what is it worth per hour", built by the server every half hour.
func (s *server) apiQueue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampInt(q.Get("limit"), 20, 1, 100)
	// The page's chips are filters here too: an endpoint that silently ignored
	// them answered a different question than the one asked.
	filter := store.QueueFilter{
		Platform:      q.Get("platform"),
		Kind:          q.Get("kind"),
		Paying:        truthy(q.Get("paying")),
		OnePerProgram: truthy(q.Get("one")),
	}
	if v := q.Get("added"); v != "" {
		// A window that does not parse used to be ignored in silence, so a typo
		// showed the whole queue and looked like a working filter.
		if _, ok := store.AddedDays(v); !ok {
			writeJSONError(w, http.StatusBadRequest, "added must be a number of days, like 7d")
			return
		}
		filter.Added = v
	}
	rows, computed, err := s.Store.HuntQueueTop(r.Context(), filter, limit, 0)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	// ?explain=1 adds each row's factor breakdown: what moved its score.
	explain := q.Get("explain") == "1"
	out := apiQueueJSON{ComputedAt: computed.UTC().Format("2006-01-02T15:04:05Z"), Rows: make([]apiQueueRow, 0, len(rows))}
	for _, row := range rows {
		item := apiQueueRow{
			Platform: row.Platform, Handle: row.Handle, Kind: row.Kind, Value: row.Value,
			Apex: row.Apex, Score: row.Score, Reasons: huntqueue.English(row.Reasons),
		}
		if explain {
			for _, f := range row.Factors {
				item.Factors = append(item.Factors, apiQueueFactor{f.Name, f.Value, f.Weight, f.Effect})
			}
		}
		out.Rows = append(out.Rows, item)
	}
	gated, gatedErr := s.Store.HuntQueueGated(r.Context(), store.QueueFilter{}, 20)
	if gatedErr != nil {
		// The row list above is still an honest answer; this extra section is
		// not, so say so in the log rather than sending an empty list that reads as
		// "nothing is gated".
		s.Log.WarnContext(r.Context(), "api: cannot read the gated queue rows", "error", gatedErr)
	} else {
		for _, row := range gated {
			out.Gated = append(out.Gated, apiQueueRow{
				Platform: row.Platform, Handle: row.Handle, Kind: row.Kind, Value: row.Value,
				Apex: row.Apex, Score: row.Score, Reasons: huntqueue.English(row.Reasons), Gate: row.Gate,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// truthy reads a boolean query parameter the ways people write them: the page's
// chips send 1, a script may send true or yes, and anything else is off.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
