package store

import (
	"context"
	"errors"
	"fmt"
)

// SignedOutSetting marks a database as a signed-out seed's (ExportSeed
// with SignedOut), holding only what anyone can see without signing in.
// The export writes it and an import carries it over.
const SignedOutSetting = "seed.signed_out"

// ErrNotSignedOut is CheckSignedOut's answer for a database that isn't fit
// to show other people.
var ErrNotSignedOut = errors.New("not a signed-out seed's database")

// CheckSignedOut reports whether this database is fit to be shown to
// other people read-only (a demo): loaded from a signed-out seed, and
// holding nothing private, nothing shown only to accounts and nothing of
// an account's. Read-only mode refuses to start without it, so a full
// install's database, or a seed exported without --signed-out, is never
// served by mistake.
func (s *Store) CheckSignedOut(ctx context.Context) error {
	var marked int
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key = ?`, SignedOutSetting).Scan(&marked); err != nil {
		return err
	}
	if marked == 0 {
		return fmt.Errorf("%w: load one exported with `huntdeck seed export --signed-out`", ErrNotSignedOut)
	}
	for _, c := range []struct{ what, q string }{
		{"private, invited or joined programs", `SELECT count(*) FROM programs WHERE private <> 0 OR invited_at IS NOT NULL
			OR joined_at IS NOT NULL OR invite_pending <> 0 OR ignored <> 0`},
		{"programs shown only to accounts", `SELECT count(*) FROM programs WHERE scope_unavailable_at IS NOT NULL OR (platform = 'intigriti'
			AND COALESCE(CASE WHEN json_valid(raw) THEN json_extract(raw, '$.confidentialityLevel.id') END, 0) <> 4)`},
		{"Intigriti announcements", `SELECT count(*) FROM changes WHERE kind = 'announcement'
			AND program_id IN (SELECT id FROM programs WHERE platform = 'intigriti')`},
		{"the user's numbers, tags or thanks", `SELECT (SELECT count(*) FROM program_user_stats) + (SELECT count(*) FROM program_tags)
			+ (SELECT count(*) FROM program_thanks)`},
		{"the user's own reports", `SELECT (SELECT count(*) FROM my_reports) + (SELECT count(*) FROM my_report_payments)
			+ (SELECT count(*) FROM my_report_events) + (SELECT count(*) FROM my_report_changes)`},
		{"the user's HuntMail addresses", `SELECT (SELECT count(*) FROM mail_prefixes) + (SELECT count(*) FROM mail_addresses)`},
		{"polls or notifications", `SELECT (SELECT count(*) FROM poll_runs) + (SELECT count(*) FROM notify_channels)
			+ (SELECT count(*) FROM notify_outbox) + (SELECT count(*) FROM notify_pending)`},
		{"reporters' handles", `SELECT (SELECT count(*) FROM accepted_reports WHERE COALESCE(reporter, '') <> '')
			+ (SELECT count(*) FROM resolved_reports WHERE COALESCE(reporter, '') <> '')
			+ (SELECT count(*) FROM disclosed_reports WHERE COALESCE(reporter, '') <> '')`},
		{"settings of an install's", `SELECT count(*) FROM settings WHERE NOT (key = '` + SignedOutSetting + `' OR key = '` + ScopeVersionSetting + `'
			OR key GLOB 'disclosures.*.cursor' OR key GLOB 'resolutions.*.cursor')`},
	} {
		var n int
		if err := s.db.R.QueryRowContext(ctx, c.q).Scan(&n); err != nil {
			return fmt.Errorf("check %s: %w", c.what, err)
		}
		if n > 0 {
			return fmt.Errorf("%w: it holds %s (%d)", ErrNotSignedOut, c.what, n)
		}
	}
	return nil
}
