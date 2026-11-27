package inspectiv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher.
// It uses the full program object cached in c.meta during ListPrograms when
// available, falling back to GET /programs/{uuid}/.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	uuid := strings.TrimSpace(p.Handle)
	if uuid == "" {
		uuid = strings.TrimSpace(p.PlatformID)
	}
	if uuid == "" {
		return nil, platform.ScopeDetail{}, errors.New("inspectiv: missing program uuid")
	}

	pj, ok := c.getMeta(uuid)
	if !ok {
		if err := c.ensureSession(ctx); err != nil {
			return nil, platform.ScopeDetail{}, err
		}
		if err := c.getJSON(ctx, "/programs/"+url.PathEscape(uuid)+"/", &pj); err != nil {
			var ae *apiError
			if errors.As(err, &ae) && (ae.StatusCode == http.StatusForbidden || ae.StatusCode == http.StatusNotFound) {
				return nil, platform.ScopeDetail{}, fmt.Errorf("inspectiv: program %s: %w", uuid, platform.ErrScopeUnavailable)
			}
			return nil, platform.ScopeDetail{}, fmt.Errorf("inspectiv: program %s: %w", uuid, err)
		}
		if pj.UUID == "" {
			pj.UUID = uuid
		}
		c.setMeta(uuid, pj)
	}

	entries, detail := parseProgramScopeAndDetail(pj)
	return entries, detail, nil
}

func parseProgramScopeAndDetail(pj programJSON) ([]platform.ScopeEntry, platform.ScopeDetail) {
	rewards := extractRewards(pj)
	_, maxBounty := computeBountyBounds(pj, rewards)
	offersBounty := maxBounty > 0 || pj.Critical > 0 || pj.High > 0 || pj.Medium > 0 || pj.Low > 0

	sections := splitMarkdownSections(pj.Details)
	scopeBody := findSection(sections, "scope", "in scope")
	outScopeBody := findSection(sections, "out of scope", "out-of-scope")
	roeBody := findSection(sections, "rules of engagement", "testing rules", "rules")

	var entries []platform.ScopeEntry
	indexByKey := map[string]int{} // "inScope|normalizedIdent" -> index in entries

	addEntry := func(e platform.ScopeEntry) {
		e.Identifier = strings.TrimSpace(e.Identifier)
		if e.Identifier == "" {
			return
		}
		k := fmt.Sprintf("%t|%s", e.InScope, normalizeAssetKey(e.Identifier))
		if idx, exists := indexByKey[k]; exists {
			if e.Instruction != "" && entries[idx].Instruction == "" {
				entries[idx].Instruction = e.Instruction
			}
			return
		}
		indexByKey[k] = len(entries)
		entries = append(entries, e)
	}

	// 1. Primary in-scope hosts/wildcards from pj.Domains.
	for _, d := range pj.Domains {
		ident := strings.TrimSpace(d)
		if ident == "" {
			continue
		}
		at := classifyAsset(ident)
		addEntry(platform.ScopeEntry{
			InScope:           true,
			AssetType:         at,
			PlatformAssetType: at,
			Identifier:        ident,
			BountyEligible:    offersBounty,
		})
	}

	// 2. Enrich instructions and capture extra URL / mobile targets from # Scope.
	for _, sa := range parseScopeMarkdown(scopeBody) {
		at := classifyAsset(sa.Identifier)
		if !isValidParsedAsset(at, sa.Identifier) {
			continue
		}
		// If domains already had the bare host (e.g. "app.example.com") and
		// # Scope had "https://app.example.com" with no extra path, enrich it
		// instead of adding a duplicate host.
		if idx, matched := findMatchingDomainEntry(entries, sa.Identifier); matched {
			if sa.Instruction != "" {
				if entries[idx].Instruction == "" {
					entries[idx].Instruction = sa.Instruction
				} else if !strings.Contains(entries[idx].Instruction, sa.Instruction) {
					entries[idx].Instruction += "; " + sa.Instruction
				}
			}
			continue
		}
		addEntry(platform.ScopeEntry{
			InScope:           true,
			AssetType:         at,
			PlatformAssetType: at,
			Identifier:        sa.Identifier,
			BountyEligible:    offersBounty,
			Instruction:       sa.Instruction,
		})
	}

	// 3. Parse # Out of Scope into InScope: false entries.
	for _, oa := range parseOutOfScopeMarkdown(outScopeBody) {
		at := classifyAsset(oa.Identifier)
		addEntry(platform.ScopeEntry{
			InScope:           false,
			AssetType:         at,
			PlatformAssetType: at,
			Identifier:        oa.Identifier,
			Instruction:       oa.Instruction,
		})
	}

	var testing *platform.TestingRules
	if header := extractInspectivHeader(roeBody); header != "" {
		testing = &platform.TestingRules{Header: header}
	}

	detail := platform.ScopeDetail{
		Policy:  pj.Details,
		Rewards: rewards,
		Testing: testing,
		State:   mapProgramState(pj),
	}
	return entries, detail
}

func classifyAsset(ident string) string {
	low := strings.ToLower(strings.TrimSpace(ident))
	if strings.Contains(low, "play.google.com/") {
		return scope.AssetAndroid
	}
	if strings.Contains(low, "apps.apple.com/") || strings.Contains(low, "itunes.apple.com/") {
		return scope.AssetIOS
	}
	if _, err := netip.ParsePrefix(low); err == nil {
		return scope.AssetCIDR
	}
	if _, err := netip.ParseAddr(low); err == nil {
		return scope.AssetIP
	}
	return scope.WebType(ident)
}

func isValidParsedAsset(assetType, ident string) bool {
	if assetType == scope.AssetAndroid || assetType == scope.AssetIOS {
		return true
	}
	if assetType == scope.AssetOther {
		return false
	}
	return len(scope.Targets(scope.AssetOther, ident)) > 0
}

func normalizeAssetKey(ident string) string {
	s := strings.ToLower(strings.TrimSpace(ident))
	s = strings.TrimSuffix(s, "/")
	return s
}

// findMatchingDomainEntry returns the index of an existing in-scope entry
// whose bare host matches ident (when ident is the same host or a bare scheme+host
// like https://app.example.com without a subpath).
func findMatchingDomainEntry(entries []platform.ScopeEntry, ident string) (int, bool) {
	norm := normalizeAssetKey(ident)
	for i, e := range entries {
		if !e.InScope {
			continue
		}
		if normalizeAssetKey(e.Identifier) == norm {
			return i, true
		}
	}
	u, err := url.Parse(strings.TrimSpace(ident))
	if err != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return -1, false
	}
	host := strings.ToLower(u.Hostname())
	for i, e := range entries {
		if e.InScope && strings.ToLower(strings.TrimSpace(e.Identifier)) == host {
			return i, true
		}
	}
	return -1, false
}

type mdSection struct {
	Heading string
	Body    string
}

func splitMarkdownSections(md string) []mdSection {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	lines := strings.Split(md, "\n")
	var sections []mdSection
	var cur mdSection
	var body []string
	inFence := false

	flush := func() {
		if cur.Heading != "" || len(body) > 0 {
			cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
			sections = append(sections, cur)
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if heading != "" {
				flush()
				cur = mdSection{Heading: strings.ToLower(heading)}
				body = nil
				continue
			}
		}
		body = append(body, line)
	}
	flush()
	return sections
}

func findSection(sections []mdSection, names ...string) string {
	var matched []string
	for _, s := range sections {
		for _, n := range names {
			if s.Heading == n {
				if s.Body != "" {
					matched = append(matched, s.Body)
				}
				break
			}
		}
	}
	return strings.Join(matched, "\n\n")
}

type mdTable struct {
	Headers []string
	Rows    [][]string
}

func parseMarkdownTables(body string) []mdTable {
	lines := strings.Split(body, "\n")
	var tables []mdTable
	for i := 0; i < len(lines); i++ {
		if !isTableRow(lines[i]) {
			continue
		}
		if i+1 >= len(lines) || !isSeparatorRow(lines[i+1]) {
			continue
		}
		t := mdTable{Headers: splitTableRow(lines[i])}
		j := i + 2
		for j < len(lines) && isTableRow(lines[j]) {
			if !isSeparatorRow(lines[j]) {
				t.Rows = append(t.Rows, splitTableRow(lines[j]))
			}
			j++
		}
		tables = append(tables, t)
		i = j - 1
	}
	return tables
}

func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.Contains(t, "|") && len(splitTableRow(t)) >= 2
}

var sepCellRE = regexp.MustCompile(`^:?-{2,}:?$`)

func isSeparatorRow(line string) bool {
	cells := splitTableRow(line)
	if len(cells) < 2 {
		return false
	}
	for _, c := range cells {
		if !sepCellRE.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func extractRewards(pj programJSON) []platform.RewardRow {
	sections := splitMarkdownSections(pj.Details)
	sevBody := findSection(sections, "severity", "bounties", "rewards", "payouts")
	if sevBody != "" {
		if rows := parseSeverityTable(sevBody); len(rows) > 0 {
			return rows
		}
	}
	row := platform.RewardRow{
		Scope:    "Default",
		Low:      singleReward(pj.Low),
		Medium:   singleReward(pj.Medium),
		High:     singleReward(pj.High),
		Critical: singleReward(pj.Critical),
	}
	if row.Low.Max > 0 || row.Medium.Max > 0 || row.High.Max > 0 || row.Critical.Max > 0 {
		return []platform.RewardRow{row}
	}
	return nil
}

func parseSeverityTable(body string) []platform.RewardRow {
	tables := parseMarkdownTables(body)
	for _, tbl := range tables {
		if len(tbl.Headers) < 2 || len(tbl.Rows) == 0 {
			continue
		}
		tierCount := len(tbl.Headers) - 1
		rows := make([]platform.RewardRow, tierCount)
		for i := range tierCount {
			name := strings.TrimSpace(tbl.Headers[i+1])
			if name == "" {
				name = fmt.Sprintf("Tier %d", i+1)
			}
			rows[i].Scope = name
		}

		for _, r := range tbl.Rows {
			if len(r) == 0 {
				continue
			}
			sev := matchSeverityLabel(r[0])
			if sev == "" {
				continue
			}
			for i := 0; i < tierCount && i+1 < len(r); i++ {
				rr := parseAmountRange(r[i+1])
				switch sev {
				case platform.SeverityCritical:
					rows[i].Critical = rr
				case platform.SeverityHigh:
					rows[i].High = rr
				case platform.SeverityMedium:
					rows[i].Medium = rr
				case platform.SeverityLow:
					rows[i].Low = rr
				}
			}
		}

		var out []platform.RewardRow
		for _, row := range rows {
			if row.Low.Max > 0 || row.Medium.Max > 0 || row.High.Max > 0 || row.Critical.Max > 0 {
				out = append(out, row)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func matchSeverityLabel(s string) string {
	low := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(low, "critical"):
		return platform.SeverityCritical
	case strings.Contains(low, "high"):
		return platform.SeverityHigh
	case strings.Contains(low, "medium"):
		return platform.SeverityMedium
	case strings.Contains(low, "low"):
		return platform.SeverityLow
	default:
		return ""
	}
}

var amountNumRE = regexp.MustCompile(`\d+(?:,\d{3})*(?:\.\d+)?`)

func parseAmountRange(cell string) platform.RewardRange {
	matches := amountNumRE.FindAllString(cell, -1)
	if len(matches) == 0 {
		return platform.RewardRange{}
	}
	var vals []float64
	for _, m := range matches {
		v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
		if err == nil && v > 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return platform.RewardRange{}
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if len(vals) >= 2 && lo < hi {
		return platform.RewardRange{Min: lo, Max: hi}
	}
	return platform.RewardRange{Max: hi}
}

func singleReward(v float64) platform.RewardRange {
	if v <= 0 {
		return platform.RewardRange{}
	}
	return platform.RewardRange{Max: v}
}

func computeBountyBounds(pj programJSON, rewards []platform.RewardRow) (lo, hi float64) {
	consider := func(v float64) {
		if v <= 0 {
			return
		}
		if lo == 0 || v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	for _, row := range rewards {
		for _, rr := range []platform.RewardRange{row.Low, row.Medium, row.High, row.Critical} {
			consider(rr.Min)
			consider(rr.Max)
		}
	}
	for _, v := range []float64{pj.Low, pj.Medium, pj.High, pj.Critical} {
		consider(v)
	}
	return lo, hi
}

type parsedScopeAsset struct {
	Identifier  string
	Instruction string
}

func parseScopeMarkdown(body string) []parsedScopeAsset {
	if body == "" {
		return nil
	}
	var out []parsedScopeAsset
	tables := parseMarkdownTables(body)
	for _, tbl := range tables {
		assetCol, tierCol, descCol, credCol := detectScopeColumns(tbl.Headers)
		if assetCol < 0 {
			continue
		}
		for _, r := range tbl.Rows {
			if assetCol >= len(r) {
				continue
			}
			ident := cleanMarkdownAssetCell(r[assetCol])
			if ident == "" {
				continue
			}
			var parts []string
			if tierCol >= 0 && tierCol < len(r) {
				if v := cleanNoteCell(r[tierCol]); v != "" {
					parts = append(parts, v)
				}
			}
			if descCol >= 0 && descCol < len(r) {
				if v := cleanNoteCell(r[descCol]); v != "" {
					parts = append(parts, v)
				}
			}
			if credCol >= 0 && credCol < len(r) {
				if v := cleanNoteCell(r[credCol]); v != "" {
					parts = append(parts, "Credentials: "+v)
				}
			}
			out = append(out, parsedScopeAsset{
				Identifier:  ident,
				Instruction: strings.Join(parts, " — "),
			})
		}
	}

	// Also pick up bulleted targets outside tables (e.g. mobile store links or extra URLs).
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if isTableRow(trimmed) || isSeparatorRow(trimmed) {
			continue
		}
		if !isBulletLine(trimmed) {
			continue
		}
		for _, ident := range extractIdentifiersFromLine(trimmed) {
			out = append(out, parsedScopeAsset{Identifier: ident})
		}
	}
	return out
}

func detectScopeColumns(headers []string) (assetCol, tierCol, descCol, credCol int) {
	assetCol, tierCol, descCol, credCol = -1, -1, -1, -1
	for i, h := range headers {
		low := strings.ToLower(strings.TrimSpace(h))
		switch {
		case assetCol < 0 && (strings.Contains(low, "endpoint") || strings.Contains(low, "asset") || strings.Contains(low, "target") || strings.Contains(low, "domain") || strings.Contains(low, "url") || strings.Contains(low, "host")):
			assetCol = i
		case tierCol < 0 && (strings.Contains(low, "tier") || strings.Contains(low, "payout")):
			tierCol = i
		case descCol < 0 && (strings.Contains(low, "description") || strings.Contains(low, "note") || strings.Contains(low, "detail")):
			descCol = i
		case credCol < 0 && (strings.Contains(low, "credential") || strings.Contains(low, "account") || strings.Contains(low, "auth")):
			credCol = i
		}
	}
	if assetCol < 0 {
		for i, h := range headers {
			if strings.Contains(strings.ToLower(h), "scope") {
				assetCol = i
				break
			}
		}
	}
	if assetCol < 0 && len(headers) >= 2 {
		if tierCol == 0 {
			assetCol = 1
		} else {
			assetCol = 0
		}
	}
	return assetCol, tierCol, descCol, credCol
}

func parseOutOfScopeMarkdown(body string) []parsedScopeAsset {
	if body == "" {
		return nil
	}
	var out []parsedScopeAsset
	for _, tbl := range parseMarkdownTables(body) {
		assetCol, _, descCol, _ := detectScopeColumns(tbl.Headers)
		if assetCol < 0 {
			assetCol = 0
		}
		for _, r := range tbl.Rows {
			if assetCol >= len(r) {
				continue
			}
			note := ""
			if descCol >= 0 && descCol < len(r) {
				note = cleanNoteCell(r[descCol])
			}
			for _, ident := range extractIdentifiersFromLine(r[assetCol]) {
				out = append(out, parsedScopeAsset{Identifier: ident, Instruction: note})
			}
		}
	}

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isTableRow(trimmed) || isSeparatorRow(trimmed) {
			continue
		}
		for _, ident := range extractIdentifiersFromLine(trimmed) {
			out = append(out, parsedScopeAsset{Identifier: ident})
		}
	}
	return out
}

var mdLinkRE = regexp.MustCompile(`\[[^\]]*\]\((https?://[^)\s]+)\)`)

func cleanMarkdownAssetCell(cell string) string {
	s := strings.TrimSpace(cell)
	if m := mdLinkRE.FindStringSubmatch(s); len(m) == 2 {
		s = m[1]
	}
	s = strings.Trim(s, "`* ")
	// Strip inline trailing notes in parentheses if first token is the target.
	if f := strings.Fields(s); len(f) > 0 {
		cand := strings.Trim(f[0], "`*,;")
		if len(f) == 1 || isValidParsedAsset(classifyAsset(cand), cand) {
			return cand
		}
	}
	return s
}

func cleanNoteCell(cell string) string {
	s := strings.TrimSpace(cell)
	switch strings.ToLower(s) {
	case "", "-", "--", "n/a", "none", "null":
		return ""
	default:
		return s
	}
}

func isBulletLine(line string) bool {
	return strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "+ ")
}

func stripBulletPrefix(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"* ", "- ", "+ ", "• "} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return s
}

func extractIdentifiersFromLine(line string) []string {
	s := stripBulletPrefix(line)
	// Replace markdown links [label](url) with the URL directly.
	s = mdLinkRE.ReplaceAllString(s, " $1 ")
	s = strings.ReplaceAll(s, "`", " ")

	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == ';'
	}) {
		tok = strings.Trim(tok, `()[]{}<>"'.,;:`)
		if tok == "" {
			continue
		}
		at := classifyAsset(tok)
		if at == scope.AssetAndroid || at == scope.AssetIOS {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
			continue
		}
		// Strict AssetOther check ensures prose words and non-domain tokens are skipped.
		for _, t := range scope.Targets(scope.AssetOther, tok) {
			val := t.Value
			if !seen[val] {
				seen[val] = true
				out = append(out, val)
			}
		}
	}
	return out
}

var inspectivHeaderRE = regexp.MustCompile(`(?i)\b(X-Inspectiv-Tester\s*:\s*[^\s|` + "`" + `]+)`)

func extractInspectivHeader(roeBody string) string {
	if roeBody == "" {
		return ""
	}
	// First check table cells if present so trailing spaces inside a cell are preserved cleanly.
	for _, tbl := range parseMarkdownTables(roeBody) {
		for _, r := range tbl.Rows {
			for _, cell := range r {
				c := strings.Trim(strings.TrimSpace(cell), "`")
				if idx := strings.Index(strings.ToLower(c), "x-inspectiv-tester:"); idx >= 0 {
					return strings.TrimSpace(c[idx:])
				}
			}
		}
	}
	if m := inspectivHeaderRE.FindStringSubmatch(roeBody); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}
