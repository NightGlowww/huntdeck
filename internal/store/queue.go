package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/huntqueue"
)

// QueueInput is one candidate target with everything HuntQueue needs, read from
// what HuntDeck already stores. It is deliberately a plain struct: the scoring
// itself lives in internal/huntqueue, which knows no SQL.
type QueueInput struct {
	ProgramID   int64
	Platform    string
	Handle      string
	Name        string
	Kind        string
	Value       string
	Apex        string
	TargetScore float64 // the target's own score, used only to pick candidates

	ProgramValue float64 // HuntScore without freshness, 0..100
	PayoutUSD    float64
	SpeedDays    float64 // mean days from submission to acceptance, 0 when unknown
	Disclosed365 int
	InScope      int
	Flips        int
	AddedAt      time.Time
	NeedsCreds   bool // the program hands out test credentials on request
	NoAutomation bool // the program bans automated tools: hand testing only
}

// QueueCandidates returns the targets worth ranking: open programs, in scope,
// not ignored, ordered by the target score so a limit keeps the plausible ones.
// Aggregates are computed per program in the same statement; the median the
// design asks for is approximated by the mean until the backtest says whether
// the difference matters.
func (s *Store) QueueCandidates(ctx context.Context, limit int) ([]QueueInput, error) {
	if limit <= 0 {
		limit = 2000
	}
	const q = `
WITH accept_days AS (
    SELECT program_id, julianday(accepted_at) - julianday(submitted_at) AS days
      FROM accepted_reports
     WHERE submitted_at IS NOT NULL AND accepted_at >= date('now', '-90 day')
),
ranked AS (
    SELECT program_id, days,
           ROW_NUMBER() OVER (PARTITION BY program_id ORDER BY days) AS rn,
           COUNT(*)     OVER (PARTITION BY program_id)              AS n
      FROM accept_days
),
speed AS (
    SELECT program_id, AVG(days) AS median_days
      FROM ranked
     WHERE rn IN ((n + 1) / 2, (n + 2) / 2)
     GROUP BY program_id
)
SELECT t.program_id, p.platform, p.handle, p.name, t.kind, t.value, t.apex, t.score,
       -- Quality is HuntScore without freshness's contribution. Old rows have
       -- no weighted value yet, so they fall back to the raw part.
       MAX(COALESCE(hs.score - COALESCE(hs.fresh_weighted, hs.fresh), 0), 0) AS value,
       COALESCE(p.bounty_max, 0) AS payout, p.currency,
       COALESCE(sp.median_days, 0) AS speed_days,
       COALESCE((SELECT COUNT(*) FROM disclosed_reports d
                  WHERE d.program_id = t.program_id AND d.disclosed_at >= date('now', '-365 day')), 0) AS disclosed,
       COALESCE((SELECT COUNT(*) FROM scope_entries e
                  WHERE e.program_id = t.program_id AND e.in_scope = 1 AND e.removed_at IS NULL), 0) AS in_scope,
       COALESCE(p.test_no_automation, 0) AS no_automation,
       -- No stored field says "credentials must be requested" yet: the
       -- platforms' credential pools are not captured in programs.profile
       -- (checked: no program carries one). The column stays so the wiring is
       -- in place when a source exists.
       0 AS needs_creds,
       COALESCE((SELECT COUNT(*) FROM changes c
                  WHERE c.program_id = t.program_id AND c.kind = 'state_changed'
                    AND c.occurred_at >= date('now', '-30 day')), 0) AS flips,
       t.added_at
  FROM target_scores t
  JOIN programs p ON p.id = t.program_id
  LEFT JOIN hunt_scores hs ON hs.program_id = t.program_id
       AND hs.day = (SELECT MAX(day) FROM hunt_scores WHERE program_id = t.program_id)
  LEFT JOIN speed sp ON sp.program_id = t.program_id
 WHERE p.state = 'open' AND p.removed_at IS NULL AND p.ignored = 0
   AND COALESCE(p.invite_pending, 0) = 0
 ORDER BY t.score DESC
 LIMIT ?`

	rows, err := s.db.R.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("queue candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]QueueInput, 0, limit)
	for rows.Next() {
		var in QueueInput
		var added, currency string
		var payout float64
		var noAuto, needsCreds int
		if err := rows.Scan(&in.ProgramID, &in.Platform, &in.Handle, &in.Name, &in.Kind, &in.Value, &in.Apex,
			&in.TargetScore, &in.ProgramValue, &payout, &currency, &in.SpeedDays, &in.Disclosed365, &in.InScope, &noAuto, &needsCreds,
			&in.Flips, &added); err != nil {
			return nil, fmt.Errorf("queue candidates: %w", err)
		}
		in.AddedAt, _ = time.Parse(time.RFC3339, added)
		// Readiness: both are stored per program, and both make a target harder
		// to start today.
		in.NoAutomation = noAuto == 1
		in.NeedsCreds = needsCreds == 1
		// Every comparison in the queue is in dollars: the same number in yen and
		// in dollars is not the same bounty.
		in.PayoutUSD = ToUSD(payout, currency)
		out = append(out, in)
	}
	return out, rows.Err()
}

// BountyByProgram sums the bounties accepted in [from, to) per program: what a
// ranking would have earned had it been acted on at the start of the window.
func (s *Store) BountyByProgram(ctx context.Context, from, to time.Time) (map[int64]float64, error) {
	const q = `
SELECT program_id, SUM(bounty)
  FROM accepted_reports
 WHERE bounty IS NOT NULL AND bounty > 0
   AND accepted_at >= ? AND accepted_at < ?
 GROUP BY program_id`
	rows, err := s.db.R.QueryContext(ctx, q, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("bounty by program: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]float64{}
	for rows.Next() {
		var id int64
		var sum float64
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		out[id] = sum
	}
	return out, rows.Err()
}

// ScoreDays lists the days HuntScore was recorded, oldest first: the points in
// time a backtest can start from.
func (s *Store) ScoreDays(ctx context.Context) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT DISTINCT day FROM hunt_scores ORDER BY day")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var days []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// ProgramScoreAt is one program's HuntScore on a day.
type ProgramScoreAt struct {
	ProgramID int64
	Score     float64
	Fresh     float64
}

// ScoresOn reads every program's score on one day.
func (s *Store) ScoresOn(ctx context.Context, day string) ([]ProgramScoreAt, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT program_id, score, fresh FROM hunt_scores WHERE day = ?", day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramScoreAt
	for rows.Next() {
		var p ProgramScoreAt
		if err := rows.Scan(&p.ProgramID, &p.Score, &p.Fresh); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HuntQueueRow is one materialised row of the queue.
type HuntQueueRow struct {
	ProgramID int64
	Platform  string
	Handle    string
	Kind      string
	Value     string
	Apex      string
	Score     float64
	Gate      string
	Reasons   []huntqueue.Reason
	// Factors is the score's breakdown, computed with it: what each input did
	// to the number, so a reader can ask "why 68" without the inputs.
	Factors    []huntqueue.Factor
	ComputedAt time.Time
}

// MinQueueScore is the floor a ranked target has to clear to be in the queue.
// At today's data it trims nothing (the weakest candidate still scores about
// 11); it is here so a future flood of near-zero targets cannot fill the table.
const MinQueueScore = 10

// RefreshHuntQueue rebuilds the queue from the candidates: it is derived data,
// recomputed in the same pass that refreshes HuntScore, so both age together.
func (s *Store) RefreshHuntQueue(ctx context.Context, now time.Time) error {
	cands, err := s.QueueCandidates(ctx, 2000)
	if err != nil {
		return err
	}
	rows := make([]HuntQueueRow, 0, len(cands))
	belowFloor := 0
	for _, in := range cands {
		res := huntqueue.Score(huntqueue.Inputs{
			ProgramValue: in.ProgramValue,
			PayoutUSD:    in.PayoutUSD,
			MedianDays:   in.SpeedDays,
			Disclosed365: in.Disclosed365,
			InScope:      in.InScope,
			AddedAt:      in.AddedAt,
			Now:          now,
			Flips:        in.Flips,
			NeedsCreds:   in.NeedsCreds,
			NoAutomation: in.NoAutomation,
		})
		reasons, _ := json.Marshal(res.Reasons)
		// A target scoring below the floor is not a candidate for today; keeping
		// it would make the count say 2,000 when a dozen are worth a look.
		if res.Gate == huntqueue.GateNone && res.Score < MinQueueScore {
			// Counted, not just skipped: the page says how many were left out,
			// which is the difference between a shorter list and a hidden one.
			belowFloor++
			continue
		}
		_, factors := huntqueue.Explain(huntqueue.Inputs{
			ProgramValue: in.ProgramValue, PayoutUSD: in.PayoutUSD, MedianDays: in.SpeedDays,
			Disclosed365: in.Disclosed365, InScope: in.InScope, AddedAt: in.AddedAt, Now: now,
			Flips: in.Flips, NeedsCreds: in.NeedsCreds, NoAutomation: in.NoAutomation,
		})
		rows = append(rows, HuntQueueRow{
			ProgramID: in.ProgramID, Platform: in.Platform, Handle: in.Handle,
			Kind: in.Kind, Value: in.Value, Apex: in.Apex,
			Score: res.Score, Gate: string(res.Gate), Reasons: res.Reasons, Factors: factors,
			ComputedAt: now.UTC(),
		})
		_ = reasons
	}
	return s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, "DELETE FROM hunt_queue"); err != nil {
			return err
		}
		stmt, err := tx.tx.PrepareContext(ctx, `INSERT INTO hunt_queue
			(program_id, kind, value, apex, score, gate, reasons, factors, computed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		// Keep the head of the queue for today, so a backtest can later ask what
		// this table would have picked. hunt_queue itself is replaced every rebuild
		// and holds no history; the scores table is an upsert, so a day settles on
		// its last rebuild.
		if err := snapshotQueueHead(ctx, tx, rows, now); err != nil {
			return err
		}
		for _, r := range rows {
			b, _ := json.Marshal(r.Reasons)
			fb, _ := json.Marshal(r.Factors)
			if _, err := stmt.ExecContext(ctx, r.ProgramID, r.Kind, r.Value, r.Apex, r.Score, r.Gate,
				string(b), string(fb), r.ComputedAt.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		// What the list leaves out, for the footer: a shorter list that reads as
		// everything is the thing worth avoiding.
		if _, err := tx.tx.ExecContext(ctx,
			`INSERT INTO queue_dropped (day, below_floor, computed_at) VALUES (?, ?, ?)
			 ON CONFLICT (day) DO UPDATE SET below_floor = excluded.below_floor, computed_at = excluded.computed_at`,
			now.UTC().Format(time.DateOnly), belowFloor, now.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		return nil
	})
}

// QueueFilter narrows the queue: one platform, one kind of asset, or only
// programs that pay.
type QueueFilter struct {
	Platform string
	Kind     string
	Paying   bool
	// OnePerProgram keeps a program's best target only: a queue of five rows
	// from one program is not a plan for the day.
	OnePerProgram bool
	// Added keeps targets first seen within this window ("7d", "30d").
	Added string
}

func (f QueueFilter) where() (string, []any) {
	var clauses []string
	var args []any
	if f.Platform != "" {
		clauses = append(clauses, "p.platform = ?")
		args = append(args, f.Platform)
	}
	if f.Kind != "" {
		clauses = append(clauses, "q.kind = ?")
		args = append(args, f.Kind)
	}
	if f.Paying {
		clauses = append(clauses, "p.offers_bounty = 1")
	}
	// OnePerProgram is not a WHERE clause: it needs a row per program, which
	// SQL alone cannot give without either a window function over the whole
	// filtered set or a subquery that repeats the filter. HuntQueueTop dedupes
	// in Go and HuntQueueCount counts distinct programs.
	if f.Added != "" {
		if days, ok := AddedDays(f.Added); ok {
			clauses = append(clauses, "ts.added_at >= ?")
			// Stored timestamps carry milliseconds (".000Z"): formatting the
			// cutoff without them made the comparison string-wise at the boundary
			// second ("." sorts before "Z"), dropping a target exactly at it.
			args = append(args, time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02T15:04:05.000Z"))
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

// AddedDays turns "7d" into 7.
func AddedDays(v string) (int, bool) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err == nil && days > 0 {
			return days, true
		}
	}
	return 0, false
}

// HuntQueueTop reads the materialised queue, best first.
func (s *Store) HuntQueueTop(ctx context.Context, f QueueFilter, limit, offset int) ([]HuntQueueRow, time.Time, error) {
	if limit <= 0 {
		limit = 20
	}
	where, args := f.where()
	if f.OnePerProgram {
		// Fetch enough rows that the page can still be filled after one row per
		// program is kept, and dedupe in Go: a filter (kind, added, paying) has
		// to apply before the choice, or a program whose best target is filtered
		// out disappears entirely.
		args = append(args, (offset+limit)*8, 0)
		rows, err := s.huntQueueRows(ctx, where, args)
		if err != nil {
			return nil, time.Time{}, err
		}
		seen := make(map[int64]bool, len(rows))
		kept := rows[:0]
		for _, r := range rows {
			if seen[r.ProgramID] {
				continue
			}
			seen[r.ProgramID] = true
			kept = append(kept, r)
		}
		if offset >= len(kept) {
			kept = nil
		} else {
			kept = kept[offset:]
		}
		if len(kept) > limit {
			kept = kept[:limit]
		}
		computed, err := s.HuntQueueBuiltAt(ctx)
		if err != nil {
			return nil, time.Time{}, err
		}
		return kept, computed, nil
	}
	args = append(args, limit, offset)
	//nolint:gosec // where is built from constant clauses; every value is bound
	rows, err := s.db.R.QueryContext(ctx, `
SELECT q.program_id, p.platform, p.handle, q.kind, q.value, q.apex, q.score, q.gate, q.reasons, q.factors, q.computed_at
  FROM hunt_queue q JOIN programs p ON p.id = q.program_id
  LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
 WHERE q.gate = ''`+where+`
 ORDER BY q.score DESC, q.kind, q.value
 LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]HuntQueueRow, 0, limit)
	var computed time.Time
	for rows.Next() {
		var r HuntQueueRow
		var reasons, factors, at string
		if err := rows.Scan(&r.ProgramID, &r.Platform, &r.Handle, &r.Kind, &r.Value, &r.Apex,
			&r.Score, &r.Gate, &reasons, &factors, &at); err != nil {
			return nil, time.Time{}, err
		}
		_ = json.Unmarshal([]byte(factors), &r.Factors)
		// A JSON array of plain strings unmarshals into []Reason as one zero-value
		// element without an error, so the check is on the content, not the length.
		if err := json.Unmarshal([]byte(reasons), &r.Reasons); err != nil || len(r.Reasons) == 0 || r.Reasons[0].Key == "" {
			// Rows written before reasons carried keys held plain sentences.
			var plain []string
			if json.Unmarshal([]byte(reasons), &plain) == nil {
				for _, s := range plain {
					r.Reasons = append(r.Reasons, huntqueue.Reason{Key: s})
				}
			}
		}
		r.ComputedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	// The build time comes from the table, not from the page's rows: a filter
	// that matches nothing used to return the zero time, which the API then
	// printed as year 0001.
	if computed, err = s.HuntQueueBuiltAt(ctx); err != nil {
		return nil, time.Time{}, err
	}
	return out, computed, nil
}

// HuntQueueCount reports how many ranked rows the queue holds under the same
// filter the page uses, and when it was built. Gated rows are not counted: the
// pager must not promise pages that hold nothing.
func (s *Store) HuntQueueCount(ctx context.Context, f QueueFilter) (int, time.Time, error) {
	var n int
	var at any
	where, args := f.where()
	counted := "COUNT(*)"
	if f.OnePerProgram {
		// The page shows one row per program, so the count has to agree.
		counted = "COUNT(DISTINCT q.program_id)"
	}
	q := "SELECT " + counted + ", MAX(q.computed_at) FROM hunt_queue q JOIN programs p ON p.id = q.program_id" +
		" LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value" +
		" WHERE q.gate = ''" + where
	if err := s.db.R.QueryRowContext(ctx, q, args...).Scan(&n, &at); err != nil {
		return 0, time.Time{}, err
	}
	when, _ := time.Parse(time.RFC3339, fmt.Sprint(at))
	return n, when, nil
}

// HuntQueueGated lists the targets the queue left out, with the reason: what you
// cannot start today is as useful to see as what you can.
func (s *Store) HuntQueueGated(ctx context.Context, f QueueFilter, limit int) ([]HuntQueueRow, error) {
	if limit <= 0 {
		limit = 50
	}
	where, args := f.where()
	args = append(args, limit)
	//nolint:gosec // where is built from constant clauses; every value is bound
	rows, err := s.db.R.QueryContext(ctx, `
SELECT q.program_id, p.platform, p.handle, q.kind, q.value, q.apex, q.score, q.gate, q.reasons, q.factors, q.computed_at
  FROM hunt_queue q JOIN programs p ON p.id = q.program_id
  LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
 WHERE q.gate <> ''`+where+`
 ORDER BY q.gate, q.score DESC
 LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HuntQueueRow
	for rows.Next() {
		var r HuntQueueRow
		var reasons, factors, at string
		if err := rows.Scan(&r.ProgramID, &r.Platform, &r.Handle, &r.Kind, &r.Value, &r.Apex,
			&r.Score, &r.Gate, &reasons, &factors, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(reasons), &r.Reasons)
		r.ComputedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestScoreDay is the most recent day HuntScore was recorded, or "" when none.
func (s *Store) LatestScoreDay(ctx context.Context) (string, error) {
	var day string
	err := s.db.R.QueryRowContext(ctx, "SELECT COALESCE(MAX(day), '') FROM hunt_scores").Scan(&day)
	return day, err
}

// HuntQueueBuiltAt is when the queue was last rebuilt, whatever a filter
// matches: an empty result is still a built queue.
func (s *Store) HuntQueueBuiltAt(ctx context.Context) (time.Time, error) {
	var at any
	if err := s.db.R.QueryRowContext(ctx, "SELECT MAX(computed_at) FROM hunt_queue").Scan(&at); err != nil {
		return time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339, fmt.Sprint(at))
	return t, nil
}

// HuntQueueGatedCount is how many targets the queue keeps out, and why they
// are worth counting: "one invitation away" is a decision, not noise.
// HuntQueueGatedCount is how many blocked targets the same filter keeps: the
// page shows a filtered list, so an unfiltered total beside it would count
// rows the reader cannot see.
func (s *Store) HuntQueueGatedCount(ctx context.Context, f QueueFilter) (int, error) {
	where, args := f.where()
	var n int
	//nolint:gosec // where is built from constant clauses; every value is bound
	err := s.db.R.QueryRowContext(ctx, `
SELECT COUNT(*) FROM hunt_queue q JOIN programs p ON p.id = q.program_id
  LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
 WHERE q.gate <> ''`+where, args...).Scan(&n)
	return n, err
}

// HuntQueueGateCounts is how many targets each gate keeps out: "one invitation
// away" is a different decision from "needs an EEA identity".
// HuntQueueGateCounts is the same breakdown, for the same filter: a chip
// counting gates from another platform would not match the rows below it.
func (s *Store) HuntQueueGateCounts(ctx context.Context, f QueueFilter) (map[string]int, error) {
	where, args := f.where()
	//nolint:gosec // where is built from constant clauses; every value is bound
	rows, err := s.db.R.QueryContext(ctx, `
SELECT q.gate, COUNT(*) FROM hunt_queue q JOIN programs p ON p.id = q.program_id
  LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
 WHERE q.gate <> ''`+where+`
 GROUP BY q.gate ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var gate string
		var n int
		if err := rows.Scan(&gate, &n); err != nil {
			return nil, err
		}
		out[gate] = n
	}
	return out, rows.Err()
}

// QueueGate is one gate and how many targets it holds.
type QueueGate struct {
	Gate  string
	Count int
}

// currencyUSD is how many dollars one unit of each currency is worth. It is a
// fixed, deliberately rough table: the queue compares programs across platforms
// that pay in different currencies, and a yen bounty weighed as a dollar one
// ranked a ¥500,000 program as if it paid $500,000.
//
// Rates are 2026 approximations, kept here rather than fetched: a ranking that
// changed with a live FX feed would reorder itself between runs for no reason.
var currencyUSD = map[string]float64{
	"USD": 1,
	"EUR": 1.08,
	"GBP": 1.27,
	"CAD": 0.73,
	"AUD": 0.66,
	"NZD": 0.60,
	"CHF": 1.12,
	"SEK": 0.095,
	"NOK": 0.094,
	"DKK": 0.145,
	"PLN": 0.25,
	"CZK": 0.043,
	"HUF": 0.0028,
	"RON": 0.22,
	"INR": 0.012,
	"JPY": 0.0064,
	"CNY": 0.14,
	"KRW": 0.00073,
	"SGD": 0.74,
	"HKD": 0.128,
	"BRL": 0.20,
	"MXN": 0.05,
	"ZAR": 0.055,
	"ILS": 0.27,
	"TRY": 0.029,
	"UAH": 0.024,
}

// ToUSD converts an amount in currency to dollars. A currency the table does
// not know is taken at face value: guessing a rate would be worse than
// overstating a program whose currency the reader can see.
func ToUSD(amount float64, currency string) float64 {
	if amount <= 0 {
		return 0
	}
	rate, ok := currencyUSD[strings.ToUpper(strings.TrimSpace(currency))]
	if !ok {
		return amount
	}
	return amount * rate
}

// huntQueueRows runs the queue query with the given WHERE text and args and
// scans the rows: the shared half of HuntQueueTop, which either pages the
// answer or dedupes it by program.
func (s *Store) huntQueueRows(ctx context.Context, where string, args []any) ([]HuntQueueRow, error) {
	//nolint:gosec // where is built from constant clauses; every value is bound
	rows, err := s.db.R.QueryContext(ctx, `
SELECT q.program_id, p.platform, p.handle, q.kind, q.value, q.apex, q.score, q.gate, q.reasons, q.factors, q.computed_at
  FROM hunt_queue q JOIN programs p ON p.id = q.program_id
  LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
 WHERE q.gate = ''`+where+`
 ORDER BY q.score DESC, q.kind, q.value
 LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]HuntQueueRow, 0, 64)
	for rows.Next() {
		var r HuntQueueRow
		var reasons, factors, at string
		if err := rows.Scan(&r.ProgramID, &r.Platform, &r.Handle, &r.Kind, &r.Value, &r.Apex,
			&r.Score, &r.Gate, &reasons, &factors, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(reasons), &r.Reasons); err != nil || len(r.Reasons) == 0 || r.Reasons[0].Key == "" {
			// Rows written before reasons carried keys held plain sentences.
			var plain []string
			if json.Unmarshal([]byte(reasons), &plain) == nil {
				r.Reasons = nil
				for _, s := range plain {
					r.Reasons = append(r.Reasons, huntqueue.Reason{Key: s})
				}
			}
		}
		r.ComputedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, r)
	}
	return out, rows.Err()
}
