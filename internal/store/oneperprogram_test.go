package store

import (
	"testing"
	"time"
)

// TestOnePerProgramKeepsTheFirstOfEach pins the filter that stops one program's
// eight hundred wildcards filling the page. The count follows the deduplicated
// list, because a total that disagrees with the rows is the kind of half-right
// this project keeps paying for.
func TestOnePerProgramKeepsTheFirstOfEach(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })

	err := s.Write(ctx, func(tx *Tx) error {
		for i, name := range []string{"acme", "globex"} {
			var id int64
			q := "INSERT INTO programs (platform, handle, name, url, private, state, offers_bounty, currency, first_seen_at, last_seen_at) " +
				"VALUES ('bugcrowd', ?, ?, 'https://example.com', 0, 'open', 1, 'USD', ?, ?) RETURNING id"
			if err := tx.tx.QueryRowContext(ctx, q, name, name, tx.now, tx.now).Scan(&id); err != nil {
				return err
			}
			n := 3
			if i == 1 {
				n = 1
			}
			e := "INSERT INTO scope_entries (program_id, entry_key, in_scope, asset_type, platform_asset_type, " +
				"identifier, bounty_eligible, content_hash, first_seen_at, updated_at, added_at) " +
				"VALUES (?, ?, 1, 'domain', 'domain', ?, 1, 'h', ?, ?, ?)"
			for j := 0; j < n; j++ {
				key := name + "-" + string(rune('a'+j))
				if _, err := tx.tx.ExecContext(ctx, e, id, key, key+".example.com", tx.now, tx.now, tx.now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	all, allTotal, err := s.assetRows(ctx, TargetFilter{Order: TargetsByNewest, AssetTypes: []string{"domain"}}, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if allTotal != 4 || len(all) != 4 {
		t.Fatalf("fixture: %d rows, total %d, want 4", len(all), allTotal)
	}

	one, total, err := s.assetRows(ctx, TargetFilter{Order: TargetsByNewest, OnePerProgram: true, AssetTypes: []string{"domain"}}, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 2 || total != 2 {
		t.Fatalf("one per program: %d rows, total %d, want 2 and 2", len(one), total)
	}
	seen := map[int64]bool{}
	for _, r := range one {
		if seen[r.Program.ID] {
			t.Errorf("program %d appears twice", r.Program.ID)
		}
		seen[r.Program.ID] = true
	}
}
