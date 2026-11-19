package store

import (
	"context"
	"testing"
)

// Two callers that disagree about a key's type don't bring the server down:
// the second one works the value out again.
func TestMemoTypeMismatch(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()
	if _, err := Memo(ctx, s, "k", func(context.Context) (int, error) { return 7, nil }); err != nil {
		t.Fatal(err)
	}
	got, err := Memo(ctx, s, "k", func(context.Context) (string, error) { return "other", nil })
	if err != nil || got != "other" {
		t.Fatalf("second caller: %q %v", got, err)
	}
}

// A memoised value follows the data: after a write, the next call works it out
// again instead of serving what it had. That is what lets the pages cache their
// counts and rows without showing the numbers from before a poll.
func TestMemoFollowsWrites(t *testing.T) {
	s := newTestStore(t, nil)
	ctx := context.Background()
	calls := 0
	programs := func() (int, error) {
		return Memo(ctx, s, "programs", func(ctx context.Context) (int, error) {
			calls++
			sums, err := s.Summary(ctx)
			if err != nil {
				return 0, err
			}
			n := 0
			for _, sm := range sums {
				n += sm.Programs
			}
			return n, nil
		})
	}

	first, err := programs()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := programs(); err != nil { // served from the memo
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the second read worked it out again: %d calls", calls)
	}

	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `INSERT INTO programs (platform, handle, name, url, state, first_seen_at, last_seen_at,
			ignored, private, offers_bounty, n_in_scope, n_out_scope, n_wildcards, n_domains, n_disclosed)
			VALUES ('hackerone', 'memo-fresh', 'Memo Fresh', 'https://example.com', 'open', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
			0, 0, 1, 0, 0, 0, 0, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	again, err := programs()
	if err != nil {
		t.Fatal(err)
	}
	if again != first+1 {
		t.Errorf("after the write the memo said %d programs, want %d", again, first+1)
	}
}
