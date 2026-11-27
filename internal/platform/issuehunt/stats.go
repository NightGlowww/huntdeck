package issuehunt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const statsBatch = 25

type hofResp struct {
	Program string            `json:"program"`
	Entries []json.RawMessage `json:"entries"`
}

type orgResp struct {
	Data struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Bio            string `json:"bio"`
		BountyApproved bool   `json:"bountyApproved"`
	} `json:"data"`
}

// FetchProgramStats implements platform.StatsFetcher.
// For each program it reads GET /programs/{slugOrId} (replySpeed/3600 hours,
// rewards, requirements), GET /programs/{id}/hof (entry count -> HackersThanked),
// and GET /organizations/{id} (bio -> Profile.Introduction).
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	orgBioCache := map[string]string{}

	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))

		for _, h := range batch {
			pj, err := c.fetchProgramDetail(ctx, h)
			if err != nil {
				if errors.Is(err, ErrAuth) || ctx.Err() != nil {
					return fmt.Errorf("issuehunt: program stats %s: %w", h, err)
				}
				if isHTTPStatus(err, 404, 403) {
					continue
				}
				return fmt.Errorf("issuehunt: program stats %s: %w", h, err)
			}

			st := platform.ProgramStats{
				Rewards: buildRewards(pj),
				Requirements: &platform.Requirements{
					Application: pj.Visibility == "application",
				},
				Facts: buildFacts(pj),
			}
			if programOffersBounty(pj) {
				st.Currency = currencyJPY
			}
			if pj.ReplySpeed != nil && *pj.ReplySpeed > 0 {
				hrs := *pj.ReplySpeed / 3600.0
				st.FirstResponseHours = &hrs
			}

			progID := firstNonEmpty(pj.ID, h)
			var hof hofResp
			if err := c.getJSON(ctx, "/programs/"+url.PathEscape(progID)+"/hof", &hof); err != nil {
				if errors.Is(err, ErrAuth) || ctx.Err() != nil {
					return fmt.Errorf("issuehunt: hof %s: %w", h, err)
				}
				if !isHTTPStatus(err, 404, 403) {
					c.log.Debug("issuehunt: hof fetch failed", "program", h, "err", err)
				}
			} else {
				n := len(hof.Entries)
				st.HackersThanked = &n
			}

			if pj.Organization != nil && strings.TrimSpace(pj.Organization.ID) != "" {
				orgID := strings.TrimSpace(pj.Organization.ID)
				bio, cached := orgBioCache[orgID]
				if !cached {
					var org orgResp
					if err := c.getJSON(ctx, "/organizations/"+url.PathEscape(orgID), &org); err != nil {
						if errors.Is(err, ErrAuth) || ctx.Err() != nil {
							return fmt.Errorf("issuehunt: organization %s: %w", orgID, err)
						}
						if !isHTTPStatus(err, 404, 403) {
							c.log.Debug("issuehunt: organization fetch failed", "org_id", orgID, "err", err)
						}
					} else {
						bio = strings.TrimSpace(org.Data.Bio)
						orgBioCache[orgID] = bio
					}
				}
				if bio != "" {
					st.Profile.Introduction = bio
				}
			}

			out[h] = st
		}

		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}
