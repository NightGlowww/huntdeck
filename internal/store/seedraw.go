package store

import (
	"sort"
	"strings"
)

// A seed keeps only the fields of a program's raw platform JSON listed
// here: what the platform shows anyone about the program. Anything else,
// such as a field a platform adds tomorrow about the account that fetched
// it, is left out until someone lists it. Raw from a platform not listed
// isn't kept at all. Intigriti's confidentialityLevel must stay: it's how
// a signed-out seed knows a program is public (seedSignedOut,
// CheckSignedOut).
var seedRawFields = map[string][]string{
	"hackerone": {"id", "type", "attributes.handle", "attributes.name", "attributes.currency", "attributes.offers_bounties",
		"attributes.state", "attributes.submission_state", "attributes.started_accepting_at", "attributes.fast_payments",
		"attributes.gold_standard_safe_harbor", "attributes.open_scope", "attributes.triage_active",
		"attributes.allows_bounty_splitting", "attributes.profile_picture"},
	"bugcrowd": {"name", "briefUrl", "tagline", "industryName", "serviceLevel", "scopeRank", "accessStatus", "isPrivate",
		"isDemo", "endsAt", "logoUrl", "rewardSummary.minReward", "rewardSummary.maxReward", "rewardSummary.summary",
		"productEngagementType.iconVariant", "productEngagementType.label"},
	"intigriti": {"id", "handle", "name", "industry", "confidentialityLevel.id", "confidentialityLevel.value", "status.id",
		"status.value", "type.id", "type.value", "minBounty.value", "minBounty.currency", "maxBounty.value",
		"maxBounty.currency", "webLinks.detail"},
	"yeswehack": {"pid", "slug", "title", "type", "public", "vdp", "bounty", "bounty_reward_min", "bounty_reward_max", "status",
		"archived", "disabled", "country", "activity_area", "business_unit.name", "business_unit.slug",
		"business_unit.currency", "average_first_response_time", "scopes_count", "reports_count", "last_update_at",
		"report_submission_cost", "report_collaboration_active", "gift", "thumbnail.url"},
	"vulbox": {"id", "project_id", "type", "project_type", "status", "task_status", "project_status",
		"task_title", "task_desc", "task_is_src", "task_website", "task_bonus_level", "task_stime", "task_etime",
		"business_type", "city_name", "hall_project_type_name", "difficult", "difficult_name",
		"reward_range", "bonus_range.low_min", "bonus_range.low_max", "bonus_range.middle_min", "bonus_range.middle_max",
		"bonus_range.high_min", "bonus_range.high_max", "bonus_range.serious_min", "bonus_range.serious_max",
		"serious_min", "serious_max", "high_min", "high_max", "middle_min", "middle_max",
		"low_min", "low_max", "tags", "goto", "src_url"},
}

// seedRaw is the seed's raw column: for each platform, a JSON object of
// its listed fields that the program's raw has (json_patch drops the
// missing ones, which come out null); {} otherwise.
var seedRaw = func() string {
	var b strings.Builder
	b.WriteString("raw=CASE WHEN NOT json_valid(raw) THEN '{}'")
	platforms := make([]string, 0, len(seedRawFields))
	for p := range seedRawFields {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	for _, p := range platforms {
		b.WriteString("\n\tWHEN platform = '" + p + "' THEN json_patch('{}', " + rawObject(seedRawFields[p], "") + ")")
	}
	b.WriteString("\n\tELSE '{}' END")
	return b.String()
}()

// rawObject is a json_object expression of fields (dotted paths) under
// prefix, nested as in the raw JSON. Paths are constants from
// seedRawFields: letters, digits and underscores.
func rawObject(fields []string, prefix string) string {
	var order []string
	children := map[string][]string{}
	for _, f := range fields {
		head, rest, nested := strings.Cut(f, ".")
		if _, seen := children[head]; !seen {
			order = append(order, head)
			children[head] = nil
		}
		if nested {
			children[head] = append(children[head], rest)
		}
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		path := prefix + k
		v := "json(raw -> '$." + path + "')"
		if len(children[k]) > 0 {
			v = rawObject(children[k], path+".")
		}
		parts = append(parts, "'"+k+"', "+v)
	}
	return "json_object(" + strings.Join(parts, ", ") + ")"
}
