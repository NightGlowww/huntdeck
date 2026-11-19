package store

// What a seed holds: every public program's platform data, so a new
// install starts with the history this one collected instead of waiting
// weeks to backfill it. Nothing of the user's goes in: no settings,
// preferences, tags, notifications, poll history, own stats or scores, and
// no hunters' handles.
//
// Only programs that are public and never were private go in, and of
// programs added by hand (platform "independent") only those the user
// chose to share (programs.in_seeds): what they typed may be their own
// notes. A program's
// first change records the visibility it was found with and every flip
// after that is a change too, so a program that was ever private (and its
// scope from then) stays out even after it goes public.
//
// Every table and column of the schema is listed here, as copied or left
// out, and TestSeedSpecCoversSchema fails when a migration adds one that
// isn't, so new data stays out of seeds until someone decides it belongs.

// seedTable is how one table is copied. Cols are copied as they are, or as
// the expression after "=" ("reporter=”"); Omit are left at their
// defaults. Where is the rows' filter, with {src} for the schema copied
// from. Replace makes rows replace ones already there (settings).
type seedTable struct {
	Name    string
	Cols    []string
	Omit    []string
	Where   string
	Replace bool
}

// seedPublic picks the programs a seed may hold; {only} is where a
// signed-out seed narrows them further (seedSignedOut).
const seedPublic = `id IN (SELECT p.id FROM {src}.programs p WHERE p.private = 0 {only} AND (p.platform <> 'independent' OR p.in_seeds = 1) AND NOT EXISTS (
	SELECT 1 FROM {src}.changes c WHERE c.program_id = p.id AND c.field = 'visibility'
	AND 'private' IN (c.old_value, c.new_value)))`

// seedOfPublic picks rows of the programs a seed may hold.
const seedOfPublic = `program_id IN (SELECT p.id FROM {src}.programs p WHERE p.private = 0 {only} AND (p.platform <> 'independent' OR p.in_seeds = 1) AND NOT EXISTS (
	SELECT 1 FROM {src}.changes c WHERE c.program_id = p.id AND c.field = 'visibility'
	AND 'private' IN (c.old_value, c.new_value)))`

// seedSignedOut narrows a seed to what anyone can see without signing in,
// for a seed shown to other people (a demo): it leaves out programs whose
// scope a platform shows only to accounts. Of Intigriti's, only "Public"
// ones (confidentialityLevel 4) stay: "Application" and "Registered" (2,
// 3) are shown to logged-in researchers only, and a program whose level
// can't be read goes too, so a change in the platform's JSON leaves
// programs out rather than in. A program whose scope the platform
// withholds until the account accepts its terms (scope_unavailable_at,
// Bugcrowd and Intigriti) goes too.
const seedSignedOut = `AND p.scope_unavailable_at IS NULL AND (p.platform <> 'intigriti' OR
	COALESCE(CASE WHEN json_valid(p.raw) THEN json_extract(p.raw, '$.confidentialityLevel.id') END, 0) = 4)`

// seedSignedOutChanges leaves out, of a signed-out seed's changes, what a
// platform shows only to accounts: Intigriti's program updates
// (announcements) come from its researcher API.
const seedSignedOutChanges = `AND NOT (kind = 'announcement' AND program_id IN (SELECT id FROM {src}.programs WHERE platform = 'intigriti'))`

// SeedOptions narrow what a seed export holds.
type SeedOptions struct {
	// SignedOut keeps only programs anyone can see without signing in
	// (seedSignedOut).
	SignedOut bool
}

// seedTables are copied in this order (parents before children).
var seedTables = []seedTable{
	{
		Name: "programs", Where: seedPublic,
		Cols: []string{"id", "platform", "handle", "platform_id", "name", "url", "private", "state", "offers_bounty", "currency",
			"fast_payments", "safe_harbor", "triage_active", "open_scope", "started_at", seedRaw, "first_seen_at", "last_seen_at",
			"removed_at", "scope_hash", "scope_synced_at", "scope_unavailable_at", "scope_changed_at", "n_in_scope", "n_out_scope",
			"n_wildcards", "n_domains", "n_urls", "n_ips", "n_cidrs", "stats_at", "policy", "profile", "req_known", "req_min_signal",
			"req_id_verified", "req_cleared", "req_application", "scope_checked_at", "n_disclosed", "disclosures_synced_at",
			"scope_history_at", "state_history_at", "resolutions_synced_at", "accepted_synced_at", "accepted_since", "rewards",
			"bounty_min", "bounty_max", "announcements_at", "req_eea", "req_two_factor", "testing_known", "test_rate_limit",
			"test_user_agent", "test_header", "test_no_automation", "test_platform_email", "collaboration", "industry", "country",
			"triage_service", "report_cost", "req_terms", "bounty_splitting", "offers_swag", "safe_harbor_level",
			"coordinated_disclosure",
			// Only shared programs added by hand are copied, so this is 1 on
			// every one there is: imports filter by it too.
			"in_seeds"},
		// joined_at, the invitation and ignored are the user's; the HuntScore is worked
		// out again from the user's preset; the thanks list isn't kept,
		// so its read time goes too and the next poll reads it afresh.
		Omit: []string{"joined_at", "invited_at", "invite_pending", "hunt_score", "hunt_detail", "ignored", "thanks_read_at"},
	},
	{
		Name: "scope_entries", Where: seedOfPublic,
		Cols: []string{"id", "program_id", "entry_key", "in_scope", "asset_type", "platform_asset_type", "identifier",
			"bounty_eligible", "max_severity", "instruction", "platform_created_at", "platform_updated_at", "content_hash",
			"first_seen_at", "updated_at", "removed_at", "added_at", "added_bound", "cvss_confidentiality", "cvss_integrity",
			"cvss_availability"},
	},
	{
		Name: "scope_targets", Where: seedOfPublic + ` AND scope_entry_id IN (SELECT id FROM {src}.scope_entries)`,
		Cols: []string{"id", "scope_entry_id", "kind", "value", "host", "apex", "program_id"},
	},
	{
		// Changes are history, never notified: the polls they came from
		// aren't copied, and they're marked notified when they happened,
		// not when the user's notifications went out.
		Name: "changes", Where: seedOfPublic + ` {changes}`,
		Cols: []string{"id", "occurred_at", "program_id", "scope_entry_id", "kind", "field", "old_value", "new_value",
			"notified_at=occurred_at"},
		Omit: []string{"poll_run_id"},
	},
	{
		Name: "program_public_stats", Where: seedOfPublic,
		Cols: []string{"program_id", "captured_at", "currency", "resolved_reports", "reports_90d", "bounties_paid",
			"bounties_paid_90d", "avg_bounty_low", "avg_bounty_high", "top_bounty_low", "top_bounty_high", "response_efficiency",
			"last_resolved_at", "hackers_thanked", "rewards", "first_response_hours", "triage_hours", "bounty_hours",
			"resolution_hours", "signal_requirement", "by_severity", "rewarded_reports", "total_reports", "reports_7d",
			"validation_hours", "reports_month", "accepted_reports", "counted_days", "hunters", "last_submission_at", "scope_rank",
			"last_accepted_at", "rewarded_90d", "points_90d", "points_all", "hunters_90d", "known_issues", "known_issues_unique"},
	},
	{
		// The API's per-field updated_at: public figures, and when they changed.
		Name: "program_values", Where: seedOfPublic,
		Cols: []string{"program_id", "field", "value", "changed_at"},
	},
	{
		Name: "disclosed_reports", Where: seedOfPublic,
		Cols: []string{"id", "program_id", "report_id", "title", "severity", "weakness", "cve_ids", "substate", "bounty",
			"submitted_at", "disclosed_at", "votes", "url", "reporter=''", "summary",
			`raw=CASE WHEN json_valid(raw) THEN json_remove(raw, '$.relationships.reporter') ELSE '{}' END`,
			"first_seen_at", "updated_at"},
	},
	{
		Name: "leaderboard_months", Where: seedOfPublic,
		Cols: []string{"program_id", "month", "points", "hunters", "capped", "fetched_at"},
	},
	{
		Name: "resolved_reports", Where: seedOfPublic,
		Cols: []string{"id", "program_id", "report_id", "submitted_at", "resolved_at", "action", "bounty", "reporter=''",
			"first_seen_at", "updated_at"},
	},
	{
		Name: "accepted_reports", Where: seedOfPublic,
		Cols: []string{"id", "program_id", "report_id", "submitted_at", "accepted_at", "resolved_at", "priority", "bounty",
			"reporter=''", "first_seen_at", "updated_at"},
	},
	{
		// Only where the platforms' all-programs feeds were read up to,
		// and the scope parser the targets came from; no preferences.
		Name: "settings", Replace: true,
		Where: `key = '` + ScopeVersionSetting + `' OR key GLOB 'disclosures.*.cursor' OR key GLOB 'resolutions.*.cursor'
			OR key = '` + SignedOutSetting + `'`, // ExportSeed sets or clears it
		Cols: []string{"key", "value", "updated_at"},
	},
}

// seedLeftOut are the tables a seed doesn't hold, and why.
var seedLeftOut = map[string]string{
	"schema_migrations":  "the seed's own migrations record its version",
	"count_version":      "kept by triggers",
	"program_user_stats": "the user's own numbers",
	"program_tags":       "the user's tags",
	"program_notes":      "the user's own notes",
	"program_thanks":     "hunters' handles",
	"hunt_scores":        "worked out from the user's preset",
	"target_scores":      "worked out from the user's preset",
	"hunt_queue":         "worked out from the user's preset and history",
	"poll_runs":          "this install's polls",
	"notify_channels":    "the user's notification setup",
	"notify_outbox":      "the user's notifications",
	"notify_pending":     "the user's notifications",
}
