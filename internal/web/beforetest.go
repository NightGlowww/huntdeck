package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mdpsec/huntdeck/internal/store"
)

// testRule is one thing to know before testing a program, for the program
// page's "Before you test" box: who may submit, what test traffic must
// carry, where accounts come from. Most come from what the platforms
// report; some only appear in the policy text and are read from it
// (FromPolicy), so the box says so.
type testRule struct {
	Kind       string // its icon: "req", "auto", "rate", "email", "header", "agent", "creds", "warn"
	Title      string
	Detail     string
	Copy       string // a value to copy (a header, a User-Agent); "" if none
	Alert      bool   // shown in red: it limits how you may test
	FromPolicy bool
}

// beforeYouTest lists p's rules, the platform's own first.
func beforeYouTest(p *store.ProgramDetail) []testRule {
	var out []testRule
	for _, r := range requires(p.Platform, p.Requirements) {
		title, detail, _ := strings.Cut(r.Title, "; ")
		out = append(out, testRule{Kind: "req", Title: upperFirst(title), Detail: upperFirst(detail)})
	}
	var std *store.ProgramStats
	if p.PublicStats != nil {
		std = p.PublicStats
		if ex := std.Profile.StandardsExclusions; len(ex) > 0 {
			names := make([]string, len(ex))
			for i, e := range ex {
				names[i] = e.Name
			}
			out = append(out, testRule{Kind: "warn", Title: fmt.Sprintf("Opts out of %d platform %s", len(ex), plural(len(ex), "standard", "standards")),
				Detail: strings.Join(names, "; ")})
		}
	}
	have := map[string]bool{}
	if t := p.Testing; t != nil {
		if t.NoAutomation {
			out = append(out, testRule{Kind: "auto", Title: "No automated tools", Detail: "Scanners and other automated tools aren't allowed.", Alert: true})
		}
		if t.RateLimit > 0 {
			out = append(out, testRule{Kind: "rate", Title: fmt.Sprintf("At most %s %s a second", strconv.FormatFloat(t.RateLimit, 'f', -1, 64), plural(int(t.RateLimit), "request", "requests")),
				Detail: "For automated tools."})
			have["rate"] = true
		}
		if t.PlatformEmail {
			out = append(out, testRule{Kind: "email", Title: "Sign up with your " + platformEmail(p.Platform) + " address", Detail: "Test accounts must use the platform's email alias."})
			have["email"] = true
		}
		if t.UserAgent != "" {
			out = append(out, testRule{Kind: "agent", Title: "User-Agent: " + t.UserAgent, Detail: "Send it with every request.", Copy: t.UserAgent})
			have["agent"] = true
		}
		if t.Header != "" {
			out = append(out, testRule{Kind: "header", Title: t.Header, Detail: "Send this header with every request.", Copy: t.Header})
			have["header"] = true
		}
	}
	for _, r := range policyRules(policyText(p)) {
		if !have[r.Kind] {
			out = append(out, r)
		}
	}
	switch {
	case len(p.CredentialAssets) > 0:
		out = append(out, testRule{Kind: "creds", Title: fmt.Sprintf("Test credentials for %d %s", len(p.CredentialAssets), plural(len(p.CredentialAssets), "asset", "assets")),
			Detail: strings.Join(p.CredentialAssets, ", ")})
	case std != nil && std.Profile.CredentialPool != nil:
		cp := std.Profile.CredentialPool
		d := "On request on " + platformLabel(p.Platform)
		if cp.Max > 0 {
			d += fmt.Sprintf(", up to %d %s each", cp.Max, plural(cp.Max, "account", "accounts"))
		}
		if !cp.Available {
			d += "; none left right now"
		}
		out = append(out, testRule{Kind: "creds", Title: "Test credentials", Detail: d + "."})
	}
	return out
}

// policyText is all of a program's own text: the policy and, where the
// platform splits it up, the rest.
func policyText(p *store.ProgramDetail) string {
	parts := []string{p.Policy}
	if p.PublicStats != nil {
		pr := p.PublicStats.Profile
		parts = append(parts, pr.Introduction, pr.InScope, pr.OutScope)
	}
	return strings.Join(parts, "\n")
}

// platformEmail is the email alias a platform gives researchers.
func platformEmail(platformName string) string {
	switch platformName {
	case "hackerone":
		return "@wearehackerone.com"
	case "bugcrowd":
		return "@bugcrowdninja.com"
	case "intigriti":
		return "@intigriti.me"
	case "yeswehack":
		return "@yeswehack.ninja"
	}
	return platformLabel(platformName)
}

var (
	// A request header a program asks for: "X-Bug-Bounty: <username>-twilio".
	policyHeaderRE = regexp.MustCompile(`\b(X-[A-Za-z0-9-]{2,40})\s*:\s*([^\s|*` + "`" + `,;]{1,80})`)
	// A User-Agent string: "User-Agent: HackerOne-<username>".
	policyAgentRE = regexp.MustCompile(`(?i)\bUser-Agent\s*(?:header\s*)?[:=]\s*` + "`?" + `([^\s|*` + "`" + `"][^|*` + "`" + `"\n]{2,100})`)
	// The platforms' email aliases.
	policyEmailRE = regexp.MustCompile(`(?i)@(wearehackerone\.com|bugcrowdninja\.com|intigriti\.me|yeswehack\.ninja)\b`)
	// A request rate: "5 requests per second", "10 req/s".
	policyRateRE = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(?:requests?|reqs?)\s*(?:per|/|a|an)\s*(second|sec|s|minute|min)\b`)
)

// policyRules reads the rules programs only state in their text: a header
// to send, a User-Agent, the platform email to sign up with, a request
// rate. Each is the first one found.
func policyRules(text string) []testRule {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []testRule
	if m := policyHeaderRE.FindStringSubmatch(text); m != nil && !strings.EqualFold(m[1], "X-Frame-Options") && !strings.EqualFold(m[1], "X-Forwarded-For") {
		h := m[1] + ": " + strings.TrimRight(m[2], ".)")
		out = append(out, testRule{Kind: "header", Title: h, Detail: "Send this header with every request.", Copy: h, FromPolicy: true})
	}
	if m := policyAgentRE.FindStringSubmatch(text); m != nil {
		ua := strings.TrimRight(strings.TrimSpace(m[1]), ".)")
		out = append(out, testRule{Kind: "agent", Title: "User-Agent: " + ua, Detail: "Send it with every request.", Copy: ua, FromPolicy: true})
	}
	if m := policyEmailRE.FindStringSubmatch(text); m != nil {
		out = append(out, testRule{Kind: "email", Title: "Sign up with your @" + strings.ToLower(m[1]) + " address", Detail: "For test accounts.", FromPolicy: true})
	}
	if m := policyRateRE.FindStringSubmatch(text); m != nil {
		unit := "second"
		if strings.HasPrefix(strings.ToLower(m[2]), "m") {
			unit = "minute"
		}
		out = append(out, testRule{Kind: "rate", Title: fmt.Sprintf("At most %s requests a %s", m[1], unit), Detail: "For automated tools.", FromPolicy: true})
	}
	out = append(out, policyProhibitionReports(text)...)
	out = append(out, policyDeadline(text)...)
	out = append(out, policyAccounts(text)...)
	return out
}

// upperFirst capitalises s's first letter.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	// Byte-slicing here would cut a multi-byte rune in half (é, ö, İ).
	return string(unicode.ToUpper(r)) + s[size:]
}

var (
	// Things a policy forbids outright: the expensive mistakes.
	policyProhibitedRE = regexp.MustCompile(`(?i)\b(?:do not|don't|never|must not|not allowed to|prohibited)[^.]{0,80}?\b(denial[- ]of[- ]service|dos attacks?|social engineering|phishing|physical (?:access|testing|attacks?)|automated (?:scanners?|scanning|tools?)|brute[- ]forc(?:e|ing)|spam|spoofing|malware|non-public (?:systems?|data)|internal (?:systems?|networks?|services?)|leaked (?:credentials?|data|tokens?)|(?:customer|third[- ]party|other)[^.]{0,24}(?:systems?|data|accounts?)|customers?\b|\bpii\b|personal (?:data|information)|destructive (?:actions?|testing)|state[- ]changing)\b`)
	// The same nouns with the prohibition after them: "denial of service is
	// prohibited" is as common as "do not run denial of service".
	policyProhibitedAfterRE = regexp.MustCompile(`(?i)\b(denial[- ]of[- ]service|dos attacks?|social engineering|phishing|physical (?:access|testing|attacks?)|automated (?:scanners?|scanning|tools?)|brute[- ]forc(?:e|ing)|spam|spoofing|malware|non-public (?:systems?|data)|internal (?:systems?|networks?)|leaked (?:credentials?|data))\b[^.]{0,40}?\b(?:is |are )?(?:prohibited|forbidden|not allowed|not permitted)\b`)
	// A deadline to report, or the window after which a report may be disclosed.
	// "within 15 days" on its own is usually a response-time promise, not a
	// deadline to report: the sentence has to be about reporting or disclosure.
	policyDeadlineRE = regexp.MustCompile(`(?i)(?:report|submit|submission|disclos)[^.]{0,40}?\bwithin\s+\d{1,3}\s*(?:day|days|hour|hours|week|weeks)\b|\b\d{1,3}[- ]day\s+disclosure\b|\bdisclos(?:e|ure)[^.]{0,40}?after\s+\d{1,3}\s*days?\b`)
	// Accounts: what a test account may be, and whose data it may touch.
	policyAccountRE = regexp.MustCompile(`(?i)\b(?:use|create|register)[^.]{0,40}?\b(test|testing|separate|dedicated)\s+(account|accounts|user|users)\b|\b(?:do not|never|must not)[^.]{0,60}?\b(real users?|production data|customer data|personal data)\b`)
)

// policyProhibitionReports turns "do not ..." sentences into alert rules.
func policyProhibitionReports(text string) []testRule {
	seen := map[string]bool{}
	var out []testRule
	matches := policyProhibitedRE.FindAllStringSubmatch(text, -1)
	matches = append(matches, policyProhibitedAfterRE.FindAllStringSubmatch(text, -1)...)
	for _, m := range matches {
		what := strings.ToLower(strings.TrimSpace(m[1]))
		if seen[what] {
			continue
		}
		seen[what] = true
		out = append(out, testRule{Kind: "warn", Title: "Policy forbids: " + what,
			Detail: excerptAround(text, m[0]), Alert: true, FromPolicy: true})
		if len(out) == 3 {
			break
		}
	}
	return out
}

// policyDeadline is the reporting window, when the text states one.
func policyDeadline(text string) []testRule {
	// Every match is considered: a policy usually promises a response time
	// before it states a reporting deadline, and the promise must not shadow it.
	for _, m := range policyDeadlineRE.FindAllString(text, -1) {
		phrase := strings.TrimSpace(m)
		// "Reports are reviewed within 15 days" is the program's response
		// promise, not a deadline for the reporter.
		low := strings.ToLower(phrase)
		var skip bool
		for _, verb := range []string{"review", "respond", "triage", "acknowledg"} {
			if strings.Contains(low, verb) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		return []testRule{{Kind: "req", Title: upperFirst(phrase), Detail: excerptAround(text, phrase), FromPolicy: true}}
	}
	return nil
}

// policyAccounts says what a test account must be.
func policyAccounts(text string) []testRule {
	if m := policyAccountRE.FindStringSubmatch(text); m != nil {
		phrase := strings.TrimSpace(m[0])
		return []testRule{{Kind: "creds", Title: upperFirst(phrase), Detail: excerptAround(text, phrase), FromPolicy: true}}
	}
	return nil
}

// excerptAround returns the sentence the match sits in, trimmed to a readable
// length: the reader wants the program's own words, not the parser's summary.
func excerptAround(text, match string) string {
	i := strings.Index(text, match)
	if i < 0 {
		// The match came from a case-insensitive regex: find it by its
		// lowercased position, which is the same length in ASCII policies.
		i = strings.Index(strings.ToLower(text), strings.ToLower(match))
	}
	if i < 0 {
		return ""
	}
	start := strings.LastIndexAny(text[:i], ".\n")
	if start < 0 {
		start = 0
	} else {
		start++
	}
	end := i + len(match)
	for end < len(text) && !strings.ContainsRune(".\n", rune(text[end])) {
		end++
	}
	s := strings.Join(strings.Fields(text[start:end]), " ")
	// A policy written in Markdown puts "### Heading" on the line the sentence
	// starts on; the markers are not part of what the rule says.
	if i := strings.Index(s, " "); i > 0 && strings.Trim(s[:i], "#") == "" {
		s = strings.TrimSpace(s[i+1:])
	}
	if len(s) > 160 {
		s = s[:157] + "…"
	}
	return s
}
