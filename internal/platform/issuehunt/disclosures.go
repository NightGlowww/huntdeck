package issuehunt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

const disclosuresPageSize = 20

type pagedDisclosures struct {
	Data    []json.RawMessage `json:"data"`
	Page    int               `json:"page"`
	PerPage int               `json:"perPage"`
	Count   int               `json:"count"` // IssueHunt uses "count", NOT "total"
}

type disclosureJSON struct {
	ID           string   `json:"id"`
	Report       string   `json:"report"`
	Program      string   `json:"program"`
	Title        string   `json:"title"`
	UserSummary  string   `json:"userSummary"`
	OrgSummary   string   `json:"orgSummary"`
	Criticality  string   `json:"criticality"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"createdAt"`
	RewardAmount *float64 `json:"rewardAmount"`
	ReportedBy   *struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		UniqueName  string `json:"uniqueName"`
	} `json:"reportedBy"`
}

// FetchDisclosures implements platform.DisclosureFetcher.
// IssueHunt exposes disclosures per program at GET /programs/{id}/disclosures
// using "count" as the pagination total key. When p is nil (all-programs feed),
// there is no global endpoint, so it returns nil.
func (c *Client) FetchDisclosures(ctx context.Context, p *platform.Program, maxPages int, save platform.DisclosureSaver) error {
	if p == nil || maxPages <= 0 {
		return nil
	}

	programID, progHandle, isPoint, err := c.resolveProgramIdentity(ctx, p)
	if err != nil {
		if isHTTPStatus(err, 404) {
			return nil
		}
		return err
	}
	if programID == "" {
		return nil
	}

	for pageNo := 1; pageNo <= maxPages; pageNo++ {
		var page pagedDisclosures
		path := fmt.Sprintf("/programs/%s/disclosures?page=%d&perPage=%d", url.PathEscape(programID), pageNo, disclosuresPageSize)
		if err := c.getJSON(ctx, path, &page); err != nil {
			if isHTTPStatus(err, 404) {
				return nil
			}
			return fmt.Errorf("issuehunt: disclosures %s page %d: %w", progHandle, pageNo, err)
		}

		if len(page.Data) == 0 {
			break
		}

		out := make([]platform.Disclosure, 0, len(page.Data))
		for _, raw := range page.Data {
			var dj disclosureJSON
			if err := json.Unmarshal(raw, &dj); err != nil {
				c.log.Warn("issuehunt: skipping malformed disclosure", "program", progHandle, "err", err)
				continue
			}
			out = append(out, c.convertDisclosure(dj, progHandle, isPoint, raw))
		}

		more, err := save(out)
		if err != nil {
			return fmt.Errorf("issuehunt: save disclosures %s page %d: %w", progHandle, pageNo, err)
		}
		if !more {
			return nil
		}

		perPage := page.PerPage
		if perPage <= 0 {
			perPage = disclosuresPageSize
		}
		if page.Count > 0 {
			if (pageNo-1)*perPage+len(page.Data) >= page.Count {
				break
			}
		} else if len(page.Data) < perPage {
			break
		}
	}
	return nil
}

func (c *Client) resolveProgramIdentity(ctx context.Context, p *platform.Program) (programID, handle string, isPoint bool, err error) {
	if pj, ok := c.cachedProgram(p.Handle, p.PlatformID); ok {
		return firstNonEmpty(pj.ID, p.PlatformID, p.Handle), firstNonEmpty(p.Handle, pj.Slug, pj.ID), isPointCreditSystem(pj), nil
	}
	if p.PlatformID != "" {
		return p.PlatformID, firstNonEmpty(p.Handle, p.PlatformID), false, nil
	}
	slugOrID := strings.TrimSpace(p.Handle)
	if slugOrID == "" {
		return "", "", false, nil
	}
	pj, err := c.fetchProgramDetail(ctx, slugOrID)
	if err != nil {
		return "", "", false, err
	}
	return firstNonEmpty(pj.ID, slugOrID), firstNonEmpty(p.Handle, pj.Slug, pj.ID), isPointCreditSystem(pj), nil
}

func (c *Client) convertDisclosure(dj disclosureJSON, progHandle string, isPoint bool, raw json.RawMessage) platform.Disclosure {
	var reporter string
	if dj.ReportedBy != nil {
		reporter = firstNonEmpty(dj.ReportedBy.DisplayName, dj.ReportedBy.UniqueName)
	}
	var bounty *float64
	if !isPoint && dj.RewardAmount != nil && *dj.RewardAmount > 0 {
		amt := *dj.RewardAmount
		bounty = &amt
	}
	d := platform.Disclosure{
		ID:            firstNonEmpty(dj.Report, dj.ID),
		ProgramHandle: progHandle,
		Title:         strings.TrimSpace(dj.Title),
		Severity:      mapSeverity(dj.Criticality),
		Substate:      strings.TrimSpace(dj.Status),
		Bounty:        bounty,
		URL:           strings.TrimSuffix(c.web.String(), "/") + "/programs/" + url.PathEscape(progHandle) + "/disclosures/" + url.PathEscape(firstNonEmpty(dj.ID, dj.Report)),
		Reporter:      reporter,
		Summary:       firstNonEmpty(dj.OrgSummary, dj.UserSummary),
		Raw:           raw,
	}
	if t := parseTime(dj.CreatedAt); t != nil {
		d.DisclosedAt = *t
	}
	return d
}
