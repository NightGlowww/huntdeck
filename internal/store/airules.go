package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// AIRule is one rule a model read out of a program's policy. It is advisory:
// the deterministic extraction stays authoritative and quotes the policy's own
// words, while this is a paraphrase to check against them.
type AIRule struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

// AIReading is a cached model reading and where it came from.
type AIReading struct {
	ProgramID int64
	Hash      string
	Rules     []AIRule
	Model     string
	At        time.Time
}

// PolicyHash identifies one version of a program's policy text.
func PolicyHash(policy string) string {
	sum := sha256.Sum256([]byte(policy))
	return hex.EncodeToString(sum[:16])
}

// AIReading returns the cached reading for a program, when it is for this policy.
func (s *Store) AIReading(ctx context.Context, programID int64, hash string) (*AIReading, error) {
	var r AIReading
	var at, rules string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT program_id, policy_hash, rules, model, at FROM program_ai_rules WHERE program_id = ?`,
		programID).Scan(&r.ProgramID, &r.Hash, &rules, &r.Model, &at)
	if err != nil {
		return nil, err
	}
	if r.Hash != hash {
		return nil, nil // the policy changed since: the reading is stale
	}
	if err := json.Unmarshal([]byte(rules), &r.Rules); err != nil {
		return nil, fmt.Errorf("ai rules of program %d: %w", programID, err)
	}
	r.At, _ = time.Parse(time.RFC3339, at)
	return &r, nil
}

// SaveAIRules stores a reading, replacing whatever was there.
func (s *Store) SaveAIRules(ctx context.Context, programID int64, hash string, rules []AIRule, model string) error {
	b, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	_, err = s.db.W.ExecContext(ctx,
		`INSERT INTO program_ai_rules (program_id, policy_hash, rules, model, at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (program_id) DO UPDATE SET policy_hash = excluded.policy_hash, rules = excluded.rules,
		 model = excluded.model, at = excluded.at`,
		programID, hash, string(b), model, s.Now().UTC().Format(time.RFC3339))
	return err
}

// PoliciesWithText lists open programs that carry a policy, newest first, for
// the extractor to read.
func (s *Store) PoliciesWithText(ctx context.Context, platformName string, limit int) ([]ProgramRow, error) {
	if limit <= 0 {
		limit = 25
	}
	query := "SELECT id, platform, handle, policy FROM programs" +
		" WHERE state = 'open' AND removed_at IS NULL AND TRIM(COALESCE(policy, '')) <> ''"
	var args []any
	if platformName != "" {
		query += " AND platform = ?"
		args = append(args, platformName)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramRow
	for rows.Next() {
		var p ProgramRow
		if err := rows.Scan(&p.ID, &p.Platform, &p.Handle, &p.Policy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClearAIRules drops every cached reading: what a model said months ago, or what
// a test run put there, should be removable without opening the database.
func (s *Store) ClearAIRules(ctx context.Context) (int64, error) {
	res, err := s.db.W.ExecContext(ctx, "DELETE FROM program_ai_rules")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
