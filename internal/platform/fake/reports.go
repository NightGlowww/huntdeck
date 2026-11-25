package fake

import (
	"context"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Reporting is a fake platform that also lists the account's own reports.
type Reporting struct {
	*Platform
	Reports  []platform.Report
	Err      error // returned after saving the reports
	Partial  bool  // the listing isn't complete
	Detailed []string
	Asks     int
}

// ReportsConfigured implements platform.ReportFetcher.
func (r *Reporting) ReportsConfigured() bool { return true }

// FetchReports implements platform.ReportFetcher: each report is detailed
// (Events and Payments whole) when its marker isn't known, up to maxDetails.
func (r *Reporting) FetchReports(ctx context.Context, known map[string]string, maxDetails int, save platform.ReportsSaver) (bool, error) {
	r.Asks++
	r.Detailed = nil
	var out []platform.Report
	for _, rep := range r.Reports {
		if known[rep.Key] != rep.Marker && len(r.Detailed) < maxDetails {
			rep.Detailed, rep.PaymentsKnown = true, true
			r.Detailed = append(r.Detailed, rep.Key)
		} else {
			rep.Events, rep.Payments = nil, nil
		}
		out = append(out, rep)
	}
	if len(out) > 0 {
		if err := save(ctx, out); err != nil {
			return false, err
		}
	}
	return !r.Partial, r.Err
}
