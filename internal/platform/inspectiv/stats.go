package inspectiv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const statsBatch = 25

// FetchProgramStats implements platform.StatsFetcher.
// It maps the deduplicated hall_of_fame username count to HackersThanked and
// includes the program's parsed Rewards table.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	if err := c.ensureSession(ctx); err != nil {
		return fmt.Errorf("inspectiv: program stats: %w", err)
	}

	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))

		for _, h := range batch {
			uuid := strings.TrimSpace(h)
			if uuid == "" {
				continue
			}
			pj, ok := c.getMeta(uuid)
			if !ok {
				if err := c.getJSON(ctx, "/programs/"+url.PathEscape(uuid)+"/", &pj); err != nil {
					if errors.Is(err, ErrSessionExpired) || ctx.Err() != nil {
						return fmt.Errorf("inspectiv: stats for %s: %w", uuid, err)
					}
					var ae *apiError
					if errors.As(err, &ae) && (ae.StatusCode == http.StatusNotFound || ae.StatusCode == http.StatusForbidden) {
						continue
					}
					return fmt.Errorf("inspectiv: stats for %s: %w", uuid, err)
				}
				if pj.UUID == "" {
					pj.UUID = uuid
				}
				c.setMeta(uuid, pj)
			}

			thanked := countHallOfFame(pj.HallOfFame)
			out[h] = platform.ProgramStats{
				Currency:       currencyUSD,
				HackersThanked: &thanked,
				Rewards:        extractRewards(pj),
			}
		}

		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

func countHallOfFame(entries []hallOfFameJSON) int {
	if len(entries) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(entries))
	for _, h := range entries {
		u := strings.ToLower(strings.TrimSpace(h.Username))
		if u != "" {
			seen[u] = true
		}
	}
	return len(seen)
}
