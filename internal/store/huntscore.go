package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// HuntDetail is a program's HuntScore and how it was reached (see
// internal/huntscore). It is stored as JSON on the program.
type HuntDetail struct {
	Score      float64    `json:"score"`
	Parts      []HuntPart `json:"parts"`      // fresh, competition, payout, response
	Confidence string     `json:"confidence"` // "high", "medium" or "low": how much is known
	// Gate, if set, is why the program isn't picked ("ignored", "paused",
	// "closed" or "requirements"); its score is kept.
	Gate   string    `json:"gate,omitempty"`
	Preset string    `json:"preset"`
	At     time.Time `json:"at"`
}

// HuntPart is one part of a HuntScore.
type HuntPart struct {
	Key     string   `json:"key"` // "fresh", "competition", "payout" or "response"
	Score   float64  `json:"score"`
	Weight  float64  `json:"weight"`
	Known   bool     `json:"known"` // false when the platform gives nothing to judge by
	Reasons []string `json:"reasons,omitempty"`
}

// HuntScore is a scored program, to save.
type HuntScore struct {
	ProgramID int64
	Detail    HuntDetail
}

// ScoreProgram is what HuntScore reads about one live program.
type ScoreProgram struct {
	ID           int64
	Platform     string
	Private      bool
	State        string
	OffersBounty bool
	Ignored      bool // the user ignores it
	StartedAt    *time.Time
	JoinedAt     *time.Time // the user accepted its invitation
	InvitedAt    *time.Time // the invitation was sent, where the platform says
	// NewAt is when HuntDeck recorded the program as new (not part of the
	// platform's first import), and NewPrivate whether it came as private.
	NewAt      *time.Time
	NewPrivate bool
	ResumedAt  *time.Time // last went from paused or closed to open

	InScope, Wildcards, Domains, URLs, IPs, CIDRs int
	Rewards                                       []platform.RewardRow // nil when unknown

	Currency     string
	Reports90d   *int
	Reports7d    *int
	ReportsMonth *int
	Accepted90d  *int // Bugcrowd CrowdStream, once read
	// Rewarded90d is reports rewarded in 90 days where CrowdStream is kept
	// private (Bugcrowd; see platform.ProgramStats.RewardedLast90d), and
	// RewardedDays the days of saved counts it rests on.
	Rewarded90d        *float64
	RewardedDays       *int
	Paid90d            *float64
	AvgBounty          *float64
	TopBounty          *float64
	Efficiency         *int
	FirstResponseHours *float64
	ValidationHours    *float64
}

// ScoreEntry is an in-scope entry added recently.
type ScoreEntry struct {
	ID          int64
	ProgramID   int64
	Identifier  string
	AssetType   string
	Bounty      bool
	MaxSeverity string
	AddedAt     time.Time
	NewRoot     bool // one of its root domains is new to the program
	// Joined says the entry was in scope when the user got access to its
	// private program, and AddedAt is when they did.
	Joined  bool
	Targets []ScoreTarget
}

// ScoreTarget is one target of a recently added entry. An Android or iOS
// app is one too: its kind is the asset type and its value the identifier.
type ScoreTarget struct {
	Kind, Value, Apex string
	Shared            int // other live programs with the same target in scope
}

// RewardsChange is a recent change of a program's bounty table.
type RewardsChange struct {
	ProgramID int64
	At        time.Time
	Old, New  []platform.RewardRow
}

// ScoreData is everything HuntScore reads.
type ScoreData struct {
	Programs []ScoreProgram
	Recent   []ScoreEntry // in-scope entries added since the window began
	// Joined are the in-scope entries of private programs the user got
	// access to since the window began that were there before (see
	// joinedEntries): new to the user, if not to the program.
	Joined  []ScoreEntry
	Rewards []RewardsChange
}

// ScoreData reads the live programs, and the entries, accepted reports and
// bounty table changes since since (the scoring window's start).
func (s *Store) ScoreData(ctx context.Context, since time.Time) (ScoreData, error) {
	var d ScoreData
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT p.id, p.platform, p.private, p.state, p.offers_bounty, p.ignored, p.started_at, p.joined_at, p.invited_at,
			(SELECT c.occurred_at || ' ' || c.new_value FROM changes c WHERE c.program_id = p.id AND c.kind = 'program_added' AND c.old_value = ''
				ORDER BY c.occurred_at DESC LIMIT 1),
			`+resumedAt+`,
			p.n_in_scope, p.n_wildcards, p.n_domains, p.n_urls, p.n_ips, p.n_cidrs, p.rewards,
			COALESCE(ps.currency, p.currency), ps.reports_90d, ps.reports_7d, ps.reports_month,
			CASE WHEN p.accepted_since IS NOT NULL THEN (SELECT COUNT(*) FROM accepted_reports a WHERE a.program_id = p.id AND a.accepted_at >= ?) END,
			ps.bounties_paid_90d, ps.avg_bounty_high, `+topBountySQL+`, ps.response_efficiency, ps.first_response_hours, ps.validation_hours,
			ps.rewarded_90d, ps.counted_days
		FROM programs p
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE p.removed_at IS NULL
		ORDER BY p.id`, ts(since))
	if err != nil {
		return d, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var p ScoreProgram
		var started, joined, invited, added, resumed, rewards sql.NullString
		var r90, r7, rm, acc, counted sql.NullInt64
		var paid, avg, top, frh, val, rewarded90 sql.NullFloat64
		var eff sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Platform, &p.Private, &p.State, &p.OffersBounty, &p.Ignored, &started, &joined, &invited,
			&added, &resumed, &p.InScope, &p.Wildcards, &p.Domains, &p.URLs, &p.IPs, &p.CIDRs, &rewards,
			&p.Currency, &r90, &r7, &rm, &acc, &paid, &avg, &top, &eff, &frh, &val, &rewarded90, &counted); err != nil {
			return d, err
		}
		p.StartedAt, p.JoinedAt, p.InvitedAt, p.ResumedAt = parseTSPtr(started), parseTSPtr(joined), parseTSPtr(invited), parseTSPtr(resumed)
		if at, vis, ok := strings.Cut(added.String, " "); ok {
			t := parseTS(at)
			p.NewAt, p.NewPrivate = &t, vis == "private"
		}
		if rewards.Valid {
			if err := json.Unmarshal([]byte(rewards.String), &p.Rewards); err != nil {
				return d, fmt.Errorf("program %d rewards: %w", p.ID, err)
			}
			if p.Rewards == nil {
				p.Rewards = []platform.RewardRow{}
			}
		}
		p.Reports90d, p.Reports7d, p.ReportsMonth, p.Accepted90d = intPtr(r90), intPtr(r7), intPtr(rm), intPtr(acc)
		if rewarded90.Valid {
			p.Rewarded90d, p.RewardedDays = floatPtr(rewarded90), intPtr(counted)
		}
		p.Paid90d, p.AvgBounty, p.TopBounty = floatPtr(paid), floatPtr(avg), floatPtr(top)
		p.Efficiency, p.FirstResponseHours, p.ValidationHours = intPtr(eff), floatPtr(frh), floatPtr(val)
		d.Programs = append(d.Programs, p)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	if d.Recent, err = s.recentEntries(ctx, since); err != nil {
		return d, err
	}
	if d.Joined, err = s.joinedEntries(ctx, since); err != nil {
		return d, err
	}
	d.Rewards, err = s.rewardsChanges(ctx, since)
	return d, err
}

func (s *Store) rewardsChanges(ctx context.Context, since time.Time) ([]RewardsChange, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT program_id, occurred_at, old_value, new_value FROM changes
		WHERE kind = ? AND occurred_at >= ? ORDER BY occurred_at`, ChangeRewardsChanged, ts(since))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RewardsChange
	for rows.Next() {
		var c RewardsChange
		var at, oldV, newV string
		if err := rows.Scan(&c.ProgramID, &at, &oldV, &newV); err != nil {
			return nil, err
		}
		c.At = parseTS(at)
		_ = json.Unmarshal([]byte(oldV), &c.Old) // a bad value reads as no table
		_ = json.Unmarshal([]byte(newV), &c.New)
		out = append(out, c)
	}
	return out, rows.Err()
}

// TargetScore is a scored target, to save.
type TargetScore struct {
	ProgramID         int64
	Kind, Value, Apex string
	Bounty            bool
	AddedAt           time.Time
	Joined            bool // AddedAt is when the user got access (ScoreEntry.Joined)
	Score             float64
	Reasons           []string
}

// SaveHuntScores stores the current program scores, the day's history row
// for each, and the target scores in place of the previous ones, in one
// transaction. Programs not scored (removed ones) lose theirs.
func (s *Store) SaveHuntScores(ctx context.Context, scores []HuntScore, targets []TargetScore) error {
	return s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM target_scores`); err != nil {
			return err
		}
		for _, t := range targets {
			b, err := json.Marshal(t.Reasons)
			if err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `
				INSERT INTO target_scores (program_id, kind, value, apex, bounty, added_at, joined, score, reasons) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (program_id, kind, value) DO UPDATE SET apex = excluded.apex, bounty = excluded.bounty,
					added_at = excluded.added_at, joined = excluded.joined, score = excluded.score, reasons = excluded.reasons
				WHERE excluded.score > target_scores.score`,
				t.ProgramID, t.Kind, t.Value, t.Apex, b2i(t.Bounty), ts(t.AddedAt), b2i(t.Joined), t.Score, string(b)); err != nil {
				return err
			}
		}
		if _, err := tx.tx.ExecContext(ctx, `UPDATE programs SET hunt_score = NULL, hunt_detail = NULL WHERE removed_at IS NOT NULL AND hunt_score IS NOT NULL`); err != nil {
			return err
		}
		for _, sc := range scores {
			b, err := json.Marshal(sc.Detail)
			if err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE programs SET hunt_score = ?, hunt_detail = ? WHERE id = ?`,
				sc.Detail.Score, string(b), sc.ProgramID); err != nil {
				return err
			}
			part := map[string]float64{}
			freshWeighted := 0.0
			for _, p := range sc.Detail.Parts {
				part[p.Key] = p.Score
				if p.Key == "fresh" {
					// What freshness actually contributed to the total: the queue
					// subtracts this, not the raw part.
					freshWeighted = p.Score * p.Weight
				}
			}
			if _, err := tx.tx.ExecContext(ctx, `
				INSERT INTO hunt_scores (program_id, day, score, fresh, competition, payout, response, fresh_weighted) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (program_id, day) DO UPDATE SET score = excluded.score, fresh = excluded.fresh,
					competition = excluded.competition, payout = excluded.payout, response = excluded.response,
					fresh_weighted = excluded.fresh_weighted`,
				sc.ProgramID, sc.Detail.At.UTC().Format(time.DateOnly), sc.Detail.Score, part["fresh"], part["competition"],
				part["payout"], part["response"], freshWeighted); err != nil {
				return err
			}
		}
		return nil
	})
}

// HuntDay is one day of a program's HuntScore history.
type HuntDay struct {
	Day         time.Time
	Score       float64
	Fresh       float64
	Competition float64
	Payout      float64
	Response    float64
}

// HuntHistory returns a program's daily scores since since, oldest first.
func (s *Store) HuntHistory(ctx context.Context, programID int64, since time.Time) ([]HuntDay, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT day, score, fresh, competition, payout, response FROM hunt_scores
		WHERE program_id = ? AND day >= ? ORDER BY day`, programID, since.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HuntDay
	for rows.Next() {
		var h HuntDay
		var day string
		if err := rows.Scan(&day, &h.Score, &h.Fresh, &h.Competition, &h.Payout, &h.Response); err != nil {
			return nil, err
		}
		if h.Day, err = time.Parse(time.DateOnly, day); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HuntPick is a program for the hunt board's picks.
type HuntPick struct {
	ProgramRef
	OffersBounty bool
	Detail       *HuntDetail
	Change       float64 // score change over the period, for rising programs
	InScope      int     // in-scope entries
	Wildcards    int
	// TopBounty is the highest amount its bounty table, listing or stats
	// show (TopBounty);
	// AvgBounty the stats' average, for platforms that share only that.
	TopBounty, AvgBounty *float64
	Currency             string
}

// huntPickCols are the columns huntPicks scans, after the score change;
// huntPickFrom joins what they need.
const (
	huntPickCols = `, p.n_in_scope, p.n_wildcards, p.rewards, ` + topBountySQL + `, ps.avg_bounty_high, COALESCE(ps.currency, p.currency)`
	huntPickFrom = ` FROM programs p LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at`
)

// TopHunts returns the open programs with the highest HuntScores that the
// user can hunt now.
func (s *Store) TopHunts(ctx context.Context, limit int) ([]HuntPick, error) {
	return s.huntPicks(ctx, `SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty, p.hunt_detail, NULL`+
		huntPickCols+huntPickFrom+` WHERE p.removed_at IS NULL AND p.state = 'open' AND p.hunt_score IS NOT NULL
			AND COALESCE(json_extract(p.hunt_detail, '$.gate'), '') = ''
		ORDER BY p.hunt_score DESC, p.id LIMIT ?`, limit)
}

// RisingHunts returns huntable programs whose score rose most since since
// (compared with their latest score on or before that day).
func (s *Store) RisingHunts(ctx context.Context, since time.Time, limit int) ([]HuntPick, error) {
	return s.huntPicks(ctx, `
		WITH before AS (
			SELECT h.program_id, h.score FROM hunt_scores h
			WHERE h.day = (SELECT MAX(h2.day) FROM hunt_scores h2 WHERE h2.program_id = h.program_id AND h2.day <= ?)
		)
		SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty, p.hunt_detail, p.hunt_score - b.score`+
		huntPickCols+huntPickFrom+` JOIN before b ON b.program_id = p.id
		WHERE p.removed_at IS NULL AND p.state = 'open' AND p.hunt_score IS NOT NULL
			AND COALESCE(json_extract(p.hunt_detail, '$.gate'), '') = '' AND p.hunt_score - b.score >= 1
		ORDER BY p.hunt_score - b.score DESC, p.id LIMIT ?`, since.UTC().Format(time.DateOnly), limit)
}

func (s *Store) huntPicks(ctx context.Context, q string, args ...any) ([]HuntPick, error) {
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HuntPick
	for rows.Next() {
		var h HuntPick
		var detail string
		var change, statsTop, statsAvg sql.NullFloat64
		var rewards, currency sql.NullString
		if err := rows.Scan(&h.ID, &h.Platform, &h.Handle, &h.Name, &h.URL, &h.Private, &h.State, &h.OffersBounty, &detail, &change,
			&h.InScope, &h.Wildcards, &rewards, &statsTop, &statsAvg, &currency); err != nil {
			return nil, err
		}
		var table []platform.RewardRow
		if rewards.Valid {
			_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
		}
		h.TopBounty, h.AvgBounty, h.Currency = topBounty(table, floatPtr(statsTop)), floatPtr(statsAvg), currency.String
		h.Detail = new(HuntDetail)
		if err := json.Unmarshal([]byte(detail), h.Detail); err != nil {
			return nil, fmt.Errorf("program %d hunt detail: %w", h.ID, err)
		}
		h.Change = change.Float64
		out = append(out, h)
	}
	return out, rows.Err()
}

// TopReason is the main reason for a score: the first reason of the known
// part that adds the most to it.
func (d *HuntDetail) TopReason() string {
	best, bestV := "", -1.0
	for _, p := range d.Parts {
		if v := p.Weight * p.Score; p.Known && len(p.Reasons) > 0 && v > bestV {
			best, bestV = p.Reasons[0], v
		}
	}
	return best
}

// parseHunt reads a stored hunt_detail, or nil if there is none.
func parseHunt(s sql.NullString) (*HuntDetail, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var d HuntDetail
	if err := json.Unmarshal([]byte(s.String), &d); err != nil {
		return nil, fmt.Errorf("hunt detail: %w", err)
	}
	return &d, nil
}
