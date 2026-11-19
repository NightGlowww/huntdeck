package store

import (
	"context"
	"sort"
	"time"
)

// queueSnapshotHead is how much of the queue is kept per day. A backtest asks
// what the top ten would have been, so a few hundred is plenty and a year of
// them stays small.
const queueSnapshotHead = 300

// snapshotQueueHead writes today's best rows into queue_scores. It runs inside
// the rebuild's transaction: the table is replaced wholesale every half hour, so
// without this there is no record of what the queue said yesterday.
func snapshotQueueHead(ctx context.Context, tx *Tx, rows []HuntQueueRow, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	head := make([]HuntQueueRow, 0, queueSnapshotHead)
	head = append(head, rows...)
	sort.SliceStable(head, func(i, j int) bool { return head[i].Score > head[j].Score })
	if len(head) > queueSnapshotHead {
		head = head[:queueSnapshotHead]
	}
	day := now.UTC().Format(time.DateOnly)
	stmt, err := tx.tx.PrepareContext(ctx, `INSERT INTO queue_scores (day, program_id, kind, value, score)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (day, program_id, kind, value) DO UPDATE SET score = excluded.score`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, r := range head {
		if _, err := stmt.ExecContext(ctx, day, r.ProgramID, r.Kind, r.Value, r.Score); err != nil {
			return err
		}
	}
	return nil
}

// QueueDays are the days a queue snapshot exists for, oldest first.
func (s *Store) QueueDays(ctx context.Context) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT DISTINCT day FROM queue_scores ORDER BY day`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// QueueScore is one target as a day's queue had it.
type QueueScore struct {
	ProgramID int64
	Kind      string
	Value     string
	Score     float64
}

// QueueOn is a day's queue head, best first.
func (s *Store) QueueOn(ctx context.Context, day string) ([]QueueScore, error) {
	rows, err := s.db.R.QueryContext(ctx,
		`SELECT program_id, kind, value, score FROM queue_scores WHERE day = ? ORDER BY score DESC`,
		day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueueScore
	for rows.Next() {
		var q QueueScore
		if err := rows.Scan(&q.ProgramID, &q.Kind, &q.Value, &q.Score); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
