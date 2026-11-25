package hackerone

// The account's own reports (platform.ReportFetcher), from the Hacker API:
//
//   - GET /hackers/me/reports?page[size]=100, every page (about one per
//     100 reports): states, dates, severity and program.
//   - GET /hackers/reports/{id} for each report whose change marker moved
//     since the last read: its activities (state and severity changes,
//     bounties, who commented when). Only the first run reads them all.
//   - GET /hackers/payments/earnings, every page (usually one): bounties,
//     bonuses and retests with their currency, by report.
//
// Never kept: vulnerability_information (the write-up), activity messages,
// attachments and anyone's name. Verified against a real account
// 2026-11-22.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ReportsConfigured implements platform.ReportFetcher: the API token is all
// it needs.
func (c *Client) ReportsConfigured() bool { return true }

type reportJSON struct {
	ID         string `json:"id"`
	Attributes struct {
		State                  string   `json:"state"`
		Title                  string   `json:"title"`
		SubmittedAt            *string  `json:"submitted_at"`
		CreatedAt              *string  `json:"created_at"`
		TriagedAt              *string  `json:"triaged_at"`
		ClosedAt               *string  `json:"closed_at"`
		BountyAwardedAt        *string  `json:"bounty_awarded_at"`
		FirstProgramActivityAt *string  `json:"first_program_activity_at"`
		LastProgramActivityAt  *string  `json:"last_program_activity_at"`
		LastReporterActivityAt *string  `json:"last_reporter_activity_at"`
		LastActivityAt         *string  `json:"last_activity_at"`
		DisclosedAt            *string  `json:"disclosed_at"`
		CVEIDs                 []string `json:"cve_ids"`
	} `json:"attributes"`
	Relationships struct {
		Program struct {
			Data struct {
				Attributes struct {
					Handle string `json:"handle"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"program"`
		Reporter struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"reporter"`
		Severity struct {
			Data *severityJSON `json:"data"`
		} `json:"severity"`
		Weakness struct {
			Data struct {
				Attributes struct {
					Name string `json:"name"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"weakness"`
		Activities struct {
			Data []activityJSON `json:"data"`
		} `json:"activities"`
	} `json:"relationships"`
}

type severityJSON struct {
	Attributes struct {
		Rating     string   `json:"rating"`
		Score      *float64 `json:"score"`
		AuthorType string   `json:"author_type"`
	} `json:"attributes"`
}

type activityJSON struct {
	Type       string `json:"type"`
	Attributes struct {
		CreatedAt    *string `json:"created_at"`
		Internal     *bool   `json:"internal"`
		BountyAmount string  `json:"bounty_amount"`
		BonusAmount  string  `json:"bonus_amount"`
	} `json:"attributes"`
	Relationships struct {
		Actor struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"actor"`
		OldSeverity struct {
			Data *severityJSON `json:"data"`
		} `json:"old_severity"`
		NewSeverity struct {
			Data *severityJSON `json:"data"`
		} `json:"new_severity"`
	} `json:"relationships"`
}

// FetchReports implements platform.ReportFetcher.
func (c *Client) FetchReports(ctx context.Context, known map[string]string, maxDetails int, save platform.ReportsSaver) (bool, error) {
	var list []reportJSON
	next := c.endpoint(fmt.Sprintf("/hackers/me/reports?page%%5Bsize%%5D=%d", pageSize))
	for pageNo := 1; next != ""; pageNo++ {
		var p struct {
			Data  []reportJSON `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.getJSON(ctx, next, &p); err != nil {
			return false, fmt.Errorf("hackerone: list reports page %d: %w", pageNo, err)
		}
		list = append(list, p.Data...)
		var err error
		if next, err = c.sameHost(p.Links.Next); err != nil {
			return false, fmt.Errorf("hackerone: list reports page %d: %w", pageNo, err)
		}
	}
	earnings, earnErr := c.reportEarnings(ctx)
	if earnErr != nil && ctx.Err() != nil {
		return false, earnErr
	}
	slices.SortStableFunc(list, func(a, b reportJSON) int {
		return strings.Compare(deref(b.Attributes.SubmittedAt), deref(a.Attributes.SubmittedAt))
	})
	var batch []platform.Report
	details := 0
	var detailErr error
	for _, rj := range list {
		r := convertReport(rj)
		if r.Key == "" || r.SubmittedAt.IsZero() {
			continue
		}
		if earnErr == nil {
			r.Payments, r.PaymentsKnown = earnings[r.Key], true
		}
		if known[r.Key] != r.Marker && details < maxDetails && detailErr == nil {
			details++
			var d struct {
				Data reportJSON `json:"data"`
			}
			if err := c.getJSON(ctx, c.endpoint("/hackers/reports/"+r.Key), &d); err != nil {
				if ctx.Err() != nil {
					return false, err
				}
				// Stop reading details (the rest wait for the next run),
				// keep saving what the listing says.
				detailErr = fmt.Errorf("hackerone: report %s: %w", r.Key, err)
			} else {
				addReportDetail(&r, d.Data)
			}
		}
		batch = append(batch, r)
		if len(batch) == 25 {
			if err := save(ctx, batch); err != nil {
				return false, err
			}
			batch = nil
		}
	}
	if len(batch) > 0 {
		if err := save(ctx, batch); err != nil {
			return false, err
		}
	}
	return true, errors.Join(detailErr, earnErr)
}

// hackerOneStates maps a report's state to HuntDeck's. needs-more-info is
// still undecided (waiting on the hunter); retesting comes after a fix.
var hackerOneStates = map[string]string{
	"new":                    platform.ReportNew,
	"needs-more-info":        platform.ReportNew,
	"pending-program-review": platform.ReportTriaged,
	"triaged":                platform.ReportAccepted,
	"resolved":               platform.ReportResolved,
	"retesting":              platform.ReportResolved,
	"duplicate":              platform.ReportDuplicate,
	"informative":            platform.ReportInformative,
	"not-applicable":         platform.ReportNotApplicable,
	"spam":                   platform.ReportSpam,
}

func reportState(s string) string {
	if st, ok := hackerOneStates[s]; ok {
		return st
	}
	return platform.ReportNew // a state added later: undecided until mapped
}

func ratingOf(s *severityJSON) string {
	if s == nil {
		return ""
	}
	switch r := s.Attributes.Rating; r {
	case platform.SeverityNone, platform.SeverityLow, platform.SeverityMedium, platform.SeverityHigh, platform.SeverityCritical:
		return r
	}
	return ""
}

// convertReport reads what the listing says about a report.
func convertReport(rj reportJSON) platform.Report {
	a := rj.Attributes
	r := platform.Report{
		Key:                   rj.ID,
		ProgramHandle:         rj.Relationships.Program.Data.Attributes.Handle,
		Title:                 a.Title,
		URL:                   "https://hackerone.com/reports/" + rj.ID,
		State:                 reportState(a.State),
		RawState:              a.State,
		TriagedAt:             parseTime(a.TriagedAt),
		ClosedAt:              parseTime(a.ClosedAt),
		FirstBountyAt:         parseTime(a.BountyAwardedAt),
		FirstResponseAt:       parseTime(a.FirstProgramActivityAt),
		LastProgramActivityAt: parseTime(a.LastProgramActivityAt),
		LastHunterActivityAt:  parseTime(a.LastReporterActivityAt),
		LastActivityAt:        latest(parseTime(a.LastActivityAt), parseTime(a.LastProgramActivityAt), parseTime(a.LastReporterActivityAt)),
	}
	if t := parseTime(a.SubmittedAt); t != nil {
		r.SubmittedAt = *t
	} else if t := parseTime(a.CreatedAt); t != nil {
		r.SubmittedAt = *t
	}
	if sev := rj.Relationships.Severity.Data; sev != nil {
		r.Severity, r.CVSS = ratingOf(sev), sev.Attributes.Score
		if sev.Attributes.AuthorType == "User" {
			// The hunter rated it; triage hasn't (or the details will say).
			r.SubmittedSeverity = r.Severity
		}
	}
	if a.State == "needs-more-info" {
		r.WaitingOn = "you"
	}
	r.Marker = strings.Join([]string{a.State, deref(a.LastActivityAt), deref(a.LastProgramActivityAt),
		deref(a.LastReporterActivityAt), deref(a.BountyAwardedAt), deref(a.ClosedAt), r.Severity}, "|")
	raw, _ := json.Marshal(map[string]any{"state": a.State, "weakness": rj.Relationships.Weakness.Data.Attributes.Name,
		"cve_ids": a.CVEIDs, "disclosed_at": a.DisclosedAt})
	r.Raw = raw
	return r
}

// hackerOneEvents names the activities that change a report's state.
var hackerOneEvents = map[string]string{
	"activity-bug-new":                    "new",
	"activity-bug-needs-more-info":        "needs-more-info",
	"activity-bug-pending-program-review": "pending-program-review",
	"activity-bug-triaged":                "triaged",
	"activity-bug-resolved":               "resolved",
	"activity-bug-duplicate":              "duplicate",
	"activity-bug-informative":            "informative",
	"activity-bug-not-applicable":         "not-applicable",
	"activity-bug-spam":                   "spam",
	"activity-bug-reopened":               "new",
	"activity-bug-retesting":              "retesting",
}

// addReportDetail adds what a report's activities say: its timeline, the
// severity it was submitted with, comments and who spoke last.
func addReportDetail(r *platform.Report, d reportJSON) {
	reporter := d.Relationships.Reporter.Data.ID
	acts := slices.Clone(d.Relationships.Activities.Data)
	slices.SortStableFunc(acts, func(a, b activityJSON) int {
		return strings.Compare(deref(a.Attributes.CreatedAt), deref(b.Attributes.CreatedAt))
	})
	r.Detailed = true
	r.Comments = 0
	firstSeverity := true
	lastComment := ""
	prevState := ""
	for _, a := range acts {
		at := parseTime(a.Attributes.CreatedAt)
		if at == nil || (a.Attributes.Internal != nil && *a.Attributes.Internal) {
			continue
		}
		mine := reporter != "" && a.Relationships.Actor.Data.ID == reporter
		switch a.Type {
		case "activity-comment":
			r.Comments++
			kind := platform.EventCommentProgram
			if mine {
				kind, lastComment = platform.EventCommentYou, "you"
			} else {
				lastComment = "program"
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: kind})
		case "activity-report-severity-updated":
			oldR, newR := ratingOf(a.Relationships.OldSeverity.Data), ratingOf(a.Relationships.NewSeverity.Data)
			if firstSeverity {
				firstSeverity = false
				if a.Relationships.OldSeverity.Data != nil {
					r.SubmittedSeverity = oldR
				}
			}
			if !mine {
				r.SeverityChanged = true
			}
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventSeverity, From: oldR, To: newR})
		case "activity-bounty-awarded":
			r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventBounty, To: a.Attributes.BountyAmount})
			if r.FirstBountyAt == nil || at.Before(*r.FirstBountyAt) {
				r.FirstBountyAt = at
			}
		default:
			if to, ok := hackerOneEvents[a.Type]; ok {
				r.Events = append(r.Events, platform.ReportEvent{At: *at, Kind: platform.EventState, From: prevState, To: to})
				prevState = to
				if (to == "pending-program-review" || to == "triaged") && (r.TriagedAt == nil || at.Before(*r.TriagedAt)) {
					r.TriagedAt = at
				}
			}
		}
	}
	// Waiting on you only when HackerOne says so (needs-more-info): a
	// program's last comment is as often "thanks, looking" as a question.
	if r.WaitingOn == "" && lastComment == "you" && isOpen(r.State) {
		r.WaitingOn = "program"
	}
}

// isOpen reports whether nobody has closed the report yet.
func isOpen(state string) bool {
	return state == platform.ReportNew || state == platform.ReportTriaged || state == platform.ReportAccepted
}

// reportEarnings reads the account's earnings, by report ID: bounties and
// their bonuses in the currency awarded, and retests.
func (c *Client) reportEarnings(ctx context.Context) (map[string][]platform.ReportPayment, error) {
	type earningJSON struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Amount    float64 `json:"amount"`
			CreatedAt *string `json:"created_at"`
		} `json:"attributes"`
		Relationships struct {
			Program struct {
				Data struct {
					Attributes struct {
						Currency string `json:"currency"`
					} `json:"attributes"`
				} `json:"data"`
			} `json:"program"`
			Bounty struct {
				Data struct {
					ID         string `json:"id"`
					Attributes struct {
						AwardedAmount      string  `json:"awarded_amount"`
						AwardedBonusAmount string  `json:"awarded_bonus_amount"`
						AwardedCurrency    string  `json:"awarded_currency"`
						CreatedAt          *string `json:"created_at"`
					} `json:"attributes"`
					Relationships struct {
						Report struct {
							Data struct {
								ID string `json:"id"`
							} `json:"data"`
						} `json:"report"`
					} `json:"relationships"`
				} `json:"data"`
			} `json:"bounty"`
			RetestUser struct {
				Data struct {
					Relationships struct {
						Retest struct {
							Data struct {
								Relationships struct {
									Report struct {
										Data struct {
											ID string `json:"id"`
										} `json:"data"`
									} `json:"report"`
								} `json:"relationships"`
							} `json:"data"`
						} `json:"report_retest"`
					} `json:"relationships"`
				} `json:"data"`
			} `json:"report_retest_user"`
		} `json:"relationships"`
	}
	out := map[string][]platform.ReportPayment{}
	next := c.endpoint(fmt.Sprintf("/hackers/payments/earnings?page%%5Bsize%%5D=%d", pageSize))
	for pageNo := 1; next != ""; pageNo++ {
		var p struct {
			Data  []earningJSON `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.getJSON(ctx, next, &p); err != nil {
			return nil, fmt.Errorf("hackerone: earnings page %d: %w", pageNo, err)
		}
		for _, e := range p.Data {
			at := parseTime(e.Attributes.CreatedAt)
			if at == nil {
				continue
			}
			switch e.Type {
			case "earning-bounty-earned":
				b := e.Relationships.Bounty.Data
				id := b.Relationships.Report.Data.ID
				cur := strings.ToUpper(b.Attributes.AwardedCurrency)
				if cur == "" {
					cur = strings.ToUpper(e.Relationships.Program.Data.Attributes.Currency)
				}
				if bt := parseTime(b.Attributes.CreatedAt); bt != nil {
					at = bt
				}
				amount, aerr := strconv.ParseFloat(b.Attributes.AwardedAmount, 64)
				if aerr != nil {
					amount = e.Attributes.Amount
				}
				if id == "" || cur == "" {
					continue
				}
				if amount > 0 {
					out[id] = append(out[id], platform.ReportPayment{Ref: "bounty:" + b.ID, Kind: "bounty", Amount: amount, Currency: cur, AwardedAt: *at})
				}
				if bonus, err := strconv.ParseFloat(b.Attributes.AwardedBonusAmount, 64); err == nil && bonus > 0 {
					out[id] = append(out[id], platform.ReportPayment{Ref: "bonus:" + b.ID, Kind: "bonus", Amount: bonus, Currency: cur, AwardedAt: *at})
				}
			case "earning-retest-completed":
				id := e.Relationships.RetestUser.Data.Relationships.Retest.Data.Relationships.Report.Data.ID
				cur := strings.ToUpper(e.Relationships.Program.Data.Attributes.Currency)
				if id == "" || e.Attributes.Amount <= 0 {
					continue
				}
				if cur == "" {
					cur = "USD"
				}
				out[id] = append(out[id], platform.ReportPayment{Ref: "retest:" + e.ID, Kind: "retest", Amount: e.Attributes.Amount, Currency: cur, AwardedAt: *at})
			}
		}
		var err error
		if next, err = c.sameHost(p.Links.Next); err != nil {
			return nil, fmt.Errorf("hackerone: earnings page %d: %w", pageNo, err)
		}
	}
	return out, nil
}

// latest is the latest of the times given (nil ones skipped).
func latest(ts ...*time.Time) *time.Time {
	var out *time.Time
	for _, t := range ts {
		if t != nil && (out == nil || t.After(*out)) {
			out = t
		}
	}
	return out
}
