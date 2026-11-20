package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ScanConfig is what the handoff needs from the operator beyond the endpoint:
// which template and node to scan with, how many targets per task, and which
// kinds of target are worth scanning at all.
type ScanConfig struct {
	Enabled    bool
	TemplateID string
	NodeName   string
	PerTask    int
	Kinds      []string
}

// ScanConfigRow reads the single config row; a fresh install has none.
func (s *Store) ScanConfigRow(ctx context.Context) (ScanConfig, error) {
	var c ScanConfig
	var enabled int
	var kinds, at string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT enabled, template_id, node_name, per_task, kinds, updated_at FROM scan_config WHERE id = 1`).
		Scan(&enabled, &c.TemplateID, &c.NodeName, &c.PerTask, &kinds, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanConfig{PerTask: 50}, nil
	}
	if err != nil {
		return c, err
	}
	c.Enabled = enabled == 1
	if kinds != "" {
		c.Kinds = strings.Split(kinds, ",")
	}
	return c, nil
}

// SaveScanConfig writes the single row.
func (s *Store) SaveScanConfig(ctx context.Context, c ScanConfig) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	perTask := c.PerTask
	if perTask <= 0 {
		perTask = 50
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO scan_config
		(id, enabled, template_id, node_name, per_task, kinds, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled,
		 template_id = excluded.template_id, node_name = excluded.node_name,
		 per_task = excluded.per_task, kinds = excluded.kinds, updated_at = excluded.updated_at`,
		enabled, c.TemplateID, c.NodeName, perTask, strings.Join(c.Kinds, ","),
		s.Now().UTC().Format(time.RFC3339))
	return err
}

// ScanCandidate is one in-scope target that has not been handed to a scanner.
type ScanCandidate struct {
	Target   string
	Platform string
	Handle   string // the program it belongs to: a task must not mix programs
	Name     string // what a person calls it: Intigriti handles are UUIDs
	// Host is what the queue ranks: a URL entry's identifier is the whole URL,
	// so "community.personio.com" in the queue is this entry's host.
	Host string
	Kind string
}

// ScanCandidates lists in-scope targets that no scan has covered yet, newest
// first, limited to the kinds the operator chose. A target that was handed off
// before is skipped, so a re-poll does not queue the same scan again.
func (s *Store) ScanCandidates(ctx context.Context, kinds []string, limit int) ([]ScanCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{limit}
	kindClause := ""
	if len(kinds) > 0 {
		placeholders := make([]string, 0, len(kinds))
		for _, k := range kinds {
			placeholders = append(placeholders, "?")
			args = append([]any{strings.TrimSpace(k)}, args...)
		}
		kindClause = " AND lower(e.asset_type) IN (" + strings.Join(placeholders, ",") + ")"
	}
	//nolint:gosec // placeholders are counted above; every value is bound
	rows, err := s.db.R.QueryContext(ctx, `
SELECT e.identifier, p.platform, p.handle, p.name, lower(e.asset_type),
	       COALESCE((SELECT t.host FROM scope_targets t WHERE t.scope_entry_id = e.id LIMIT 1), '')
  FROM scope_entries e
  JOIN programs p ON p.id = e.program_id
  LEFT JOIN scan_handoffs h ON h.target = e.identifier
 WHERE e.in_scope = 1 AND e.removed_at IS NULL AND h.target IS NULL
   AND p.state = 'open' AND p.removed_at IS NULL AND p.ignored = 0`+kindClause+`
 ORDER BY e.id DESC
 LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ScanCandidate
	for rows.Next() {
		var c ScanCandidate
		if err := rows.Scan(&c.Target, &c.Platform, &c.Handle, &c.Name, &c.Kind, &c.Host); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkHandedOff records that a target went to a scanner, with the task id as
// the receipt.
func (s *Store) MarkHandedOff(ctx context.Context, items []ScanCandidate, taskID string) error {
	if len(items) == 0 {
		return nil
	}
	now := s.Now().UTC().Format(time.RFC3339)
	return s.Write(ctx, func(tx *Tx) error {
		for _, c := range items {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO scan_handoffs
				(target, platform, kind, task_id, created_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (target) DO UPDATE SET task_id = excluded.task_id, created_at = excluded.created_at`,
				c.Target, c.Platform, c.Kind, taskID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ScanHandoffCount is how many targets have been handed off, and when the last
// one went.
func (s *Store) ScanHandoffCount(ctx context.Context) (int, time.Time, error) {
	var n int
	var at sql.NullString
	err := s.db.R.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(created_at) FROM scan_handoffs`).Scan(&n, &at)
	if err != nil {
		return 0, time.Time{}, err
	}
	var last time.Time
	if at.Valid {
		last, _ = time.Parse(time.RFC3339, at.String)
	}
	return n, last, nil
}

// ClearHandoffs forgets that targets were handed off, so they become candidates
// again. With names set it clears only the targets whose task matches one of
// them; without it clears everything. Returns how many rows went.
func (s *Store) ClearHandoffs(ctx context.Context, taskIDs []string) (int, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		var res sql.Result
		var err error
		if len(taskIDs) == 0 {
			res, err = tx.tx.ExecContext(ctx, `DELETE FROM scan_handoffs`)
		} else {
			placeholders := make([]string, len(taskIDs))
			args := make([]any, len(taskIDs))
			for i, id := range taskIDs {
				placeholders[i] = "?"
				args[i] = id
			}
			//nolint:gosec // the placeholders are counted above; every value is bound
			res, err = tx.tx.ExecContext(ctx, `DELETE FROM scan_handoffs WHERE task_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		}
		if err != nil {
			return fmt.Errorf("clear handoffs: %w", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// HandoffBatch is one scan task as this side recorded it: the id the scanner gave
// back, the platform, and the targets that went into it. The scanner names a task
// however it likes (an Intigriti program once arrived as a UUID), so this is the
// bridge back to a program.
func (s *Store) RecentHandoffs(ctx context.Context, limit int) (map[string]HandoffBatch, []string, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT task_id, platform, target, created_at
		  FROM scan_handoffs
		 ORDER BY created_at DESC, task_id
		 LIMIT ?`, limit*50)
	if err != nil {
		return nil, nil, fmt.Errorf("recent handoffs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byTask := map[string]HandoffBatch{}
	var order []string
	for rows.Next() {
		var task, plat, target, at string
		if err := rows.Scan(&task, &plat, &target, &at); err != nil {
			return nil, nil, err
		}
		b, ok := byTask[task]
		if !ok {
			if len(order) >= limit {
				continue
			}
			order = append(order, task)
			b = HandoffBatch{TaskID: task, Platform: plat, At: at}
		}
		b.Targets = append(b.Targets, target)
		byTask[task] = b
	}
	return byTask, order, rows.Err()
}

// HandoffBatch is one task in RecentHandoffs.
type HandoffBatch struct {
	TaskID   string
	Platform string
	Targets  []string
	At       string
}
