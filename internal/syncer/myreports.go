package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// The user's own reports (platform.ReportFetcher): listed in every full
// poll, and in quick polls once reportsEvery has passed, so a new bounty
// or a question from the program shows within the hour. Details are read
// only for reports that changed, at most reportDetailsPerRun a run (the
// first sync of a large account spreads over a few).
const (
	reportsEvery        = time.Hour
	reportDetailsPerRun = 100
)

// reportsDue reports whether this poll should read the platform's reports.
func (s *Syncer) reportsDue(ctx context.Context, p platform.Platform, mode string) (platform.ReportFetcher, bool) {
	rf, ok := p.(platform.ReportFetcher)
	if !ok || !rf.ReportsConfigured() {
		return nil, false
	}
	enabled, last, err := s.store.ReportSync(ctx, p.Name())
	if err != nil || !enabled {
		return nil, false
	}
	return rf, mode == ModeFull || last == nil || s.store.Now().Sub(*last) >= reportsEvery
}

// syncReports reads the platform's reports and stores them. Like the
// stats, a failure is reported but doesn't make the scope poll partial.
func (s *Syncer) syncReports(ctx context.Context, log *slog.Logger, rf platform.ReportFetcher, platformName string) error {
	res, err := s.readReports(ctx, rf, platformName)
	// Counts only: titles and programs stay out of logs.
	log.Info("your reports read", "reports", res.Reports, "changes", res.Changes, "gone", res.Gone, "complete", res.Complete)
	return err
}

func (s *Syncer) readReports(ctx context.Context, rf platform.ReportFetcher, platformName string) (ReportsResult, error) {
	var res ReportsResult
	known, err := s.store.ReportMarkers(ctx, platformName)
	if err != nil {
		return res, fmt.Errorf("your reports: %w", err)
	}
	var seen []string
	complete, fetchErr := rf.FetchReports(ctx, known, reportDetailsPerRun, func(ctx context.Context, rs []platform.Report) error {
		ch, err := s.store.SaveReports(ctx, platformName, rs)
		if err != nil {
			return err
		}
		for _, r := range rs {
			seen = append(seen, r.Key)
		}
		res.Reports += len(rs)
		res.Changes += len(ch)
		return nil
	})
	res.Complete = complete
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if complete {
		if res.Gone, err = s.store.MarkReportsGone(ctx, platformName, seen); err != nil {
			return res, errors.Join(fetchErr, fmt.Errorf("your reports: %w", err))
		}
	}
	if res.Reports > 0 || complete {
		if err := s.store.SetReportsSynced(ctx, platformName); err != nil {
			return res, errors.Join(fetchErr, err)
		}
	}
	if fetchErr != nil {
		return res, fmt.Errorf("your reports: %w", fetchErr)
	}
	return res, nil
}

// ReportsResult is what SyncReports read.
type ReportsResult struct {
	Reports, Changes, Gone int
	Complete               bool
}

// SyncReports reads p's reports now, outside a poll (the reports command).
func (s *Syncer) SyncReports(ctx context.Context, p platform.Platform) (ReportsResult, error) {
	rf, ok := p.(platform.ReportFetcher)
	if !ok || !rf.ReportsConfigured() {
		return ReportsResult{}, fmt.Errorf("%s: reports need a login HuntDeck doesn't have", p.Name())
	}
	return s.readReports(ctx, rf, p.Name())
}
