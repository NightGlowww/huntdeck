package inspectiv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type hallOfFameJSON struct {
	Category string `json:"category"`
	Username string `json:"username"`
	Photo    string `json:"photo"`
}

type programJSON struct {
	UUID             string           `json:"uuid"`
	Title            string           `json:"title"`
	Details          string           `json:"details"`
	ShortDescription string           `json:"short_description"`
	Private          bool             `json:"private"`
	DateCreated      string           `json:"date_created"`
	Critical         float64          `json:"critical"`
	High             float64          `json:"high"`
	Medium           float64          `json:"medium"`
	Low              float64          `json:"low"`
	Informational    float64          `json:"informational"`
	PayoutRange      string           `json:"payout_range"`
	CompanyName      string           `json:"company_name"`
	Active           bool             `json:"active"`
	Frozen           bool             `json:"frozen"`
	Featured         bool             `json:"featured"`
	Status           string           `json:"status"`
	HallOfFame       []hallOfFameJSON `json:"hall_of_fame"`
	Domains          []string         `json:"domains"`
}

// ListPrograms implements platform.Platform.
// It fetches GET /programs/all/ in a single request and caches the full program
// objects in c.meta so FetchScopeDetail avoids N+1 HTTP calls.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	if err := c.ensureSession(ctx); err != nil {
		return platform.Listing{}, err
	}

	var rawItems []json.RawMessage
	if err := c.getJSON(ctx, "/programs/all/", &rawItems); err != nil {
		return platform.Listing{}, fmt.Errorf("inspectiv: list programs: %w", err)
	}

	programs := make([]platform.Program, 0, len(rawItems))
	seen := make(map[string]bool, len(rawItems))

	for _, raw := range rawItems {
		var pj programJSON
		if err := json.Unmarshal(raw, &pj); err != nil {
			return platform.Listing{Programs: programs}, fmt.Errorf("inspectiv: decode program: %w", err)
		}
		uuid := strings.TrimSpace(pj.UUID)
		if uuid == "" || seen[uuid] {
			continue
		}
		seen[uuid] = true
		pj.UUID = uuid
		c.setMeta(uuid, pj)
		programs = append(programs, c.toProgram(pj, raw))
	}

	c.log.Info("inspectiv: listed programs", "total", len(programs))
	return platform.Listing{Programs: programs, Complete: true}, nil
}

func (c *Client) toProgram(pj programJSON, raw json.RawMessage) platform.Program {
	name := strings.TrimSpace(pj.Title)
	if name == "" {
		name = "Inspectiv #" + pj.UUID
	}

	p := platform.Program{
		Handle:     pj.UUID,
		PlatformID: pj.UUID,
		Name:       name,
		URL:        strings.TrimSuffix(c.web.String(), "/") + "/#/programs/" + url.PathEscape(pj.UUID),
		Private:    pj.Private,
		State:      mapProgramState(pj),
		Currency:   currencyUSD,
		StartedAt:  parseTime(pj.DateCreated),
		Policy:     pj.Details,
		Raw:        raw,
	}

	rewards := extractRewards(pj)
	lo, hi := computeBountyBounds(pj, rewards)
	p.OffersBounty = hi > 0 || pj.Critical > 0 || pj.High > 0 || pj.Medium > 0 || pj.Low > 0
	if hi > 0 {
		p.BountyMax = &hi
	}
	if lo > 0 {
		p.BountyMin = &lo
	}
	return p
}

func mapProgramState(pj programJSON) string {
	switch {
	case !pj.Active:
		return platform.StateClosed
	case pj.Frozen:
		return platform.StatePaused
	default:
		return platform.StateOpen
	}
}

func parseTime(raw string) *time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}
