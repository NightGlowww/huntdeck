package store

import "context"

// DeliveryStat is a channel's delivery history in three numbers.
type DeliveryStat struct {
	Sent, Failed, Waiting int
}

// DeliveryStats counts each channel's deliveries: sent, given up on, and
// still waiting.
func (s *Store) DeliveryStats(ctx context.Context) (map[int64]DeliveryStat, error) {
	rows, err := s.db.R.QueryContext(ctx, "SELECT channel_id, "+
		"COALESCE(SUM(CASE WHEN sent_at IS NOT NULL THEN 1 ELSE 0 END), 0), "+
		"COALESCE(SUM(CASE WHEN failed_at IS NOT NULL THEN 1 ELSE 0 END), 0), "+
		"COALESCE(SUM(CASE WHEN sent_at IS NULL AND failed_at IS NULL THEN 1 ELSE 0 END), 0) "+
		"FROM notify_outbox GROUP BY channel_id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]DeliveryStat{}
	for rows.Next() {
		var id int64
		var st DeliveryStat
		if err := rows.Scan(&id, &st.Sent, &st.Failed, &st.Waiting); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
