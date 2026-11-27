package vulbox

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func normalizeAssetType(category, ident string) string {
	low := strings.ToLower(strings.TrimSpace(category))
	switch {
	case strings.Contains(low, "android") || strings.Contains(low, "安卓"):
		return scope.AssetAndroid
	case strings.Contains(low, "ios") || strings.Contains(low, "苹果"):
		return scope.AssetIOS
	case strings.Contains(low, "api"):
		return scope.AssetAPI
	case strings.Contains(low, "硬件") || strings.Contains(low, "iot") || strings.Contains(low, "hardware"):
		return scope.AssetHardware
	case strings.Contains(low, "pc") || strings.Contains(low, "客户端") || strings.Contains(low, "windows"):
		return scope.AssetExecutable
	case strings.Contains(low, "ip"):
		if _, err := netip.ParsePrefix(ident); err == nil {
			return scope.AssetCIDR
		}
		if _, err := netip.ParseAddr(ident); err == nil {
			return scope.AssetIP
		}
		return scope.AssetIP
	case strings.Contains(low, "web") || strings.Contains(low, "域名") || strings.Contains(low, "网站"):
		return scope.WebType(ident)
	}
	if _, err := netip.ParsePrefix(ident); err == nil {
		return scope.AssetCIDR
	}
	if _, err := netip.ParseAddr(ident); err == nil {
		return scope.AssetIP
	}
	if t := scope.WebType(ident); t != scope.AssetOther {
		return t
	}
	return scope.AssetOther
}

var (
	outOfScopeHeadingRE = regexp.MustCompile(`(?i)(不在测试范围|测试范围外|非测试范围|不收录范围|排除范围|以下.*除外|out\s+of\s+scope)`)
	inScopeHeadingRE    = regexp.MustCompile(`(?i)(测试范围|资产范围|收录范围|in\s+scope)`)
	inlineExcludeRE     = regexp.MustCompile(`(?i)[(（]([^()（）]*(?:除外|不在范围|排除|非范围)[^()（）]*)[)）]`)
)

// separateNonASCII replaces non-ASCII runes (CJK prose, full-width punctuation)
// with spaces so ASCII hosts/URLs/IPs glued directly to Chinese text
// (e.g. "test.example.com除外", "主域名：*.example.com") tokenize cleanly for
// internal/scope.Targets.
func separateNonASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > unicode.MaxASCII {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseHTMLScope extracts targets (wildcards, domains, URLs, IPs, CIDRs) from
// VulBox's HTML task_range using internal/scope.Targets.
func parseHTMLScope(rawHTML string, bounty bool) []platform.ScopeEntry {
	md := htmlToMarkdown(rawHTML)
	if md == "" {
		return nil
	}
	var out []platform.ScopeEntry
	inScope := true
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if outOfScopeHeadingRE.MatchString(trimmed) {
			inScope = false
		} else if inScopeHeadingRE.MatchString(trimmed) && !strings.Contains(trimmed, "外") {
			inScope = true
		}

		// Handle parenthetical exclusions on an otherwise in-scope line, e.g.
		// "*.example.com (test.example.com除外)".
		excludedPart := ""
		mainLine := trimmed
		if inScope {
			if m := inlineExcludeRE.FindStringSubmatch(trimmed); m != nil {
				excludedPart = m[1]
				mainLine = inlineExcludeRE.ReplaceAllString(trimmed, " ")
			}
		}

		for _, t := range scope.Targets(scope.AssetOther, separateNonASCII(mainLine)) {
			out = append(out, platform.ScopeEntry{
				InScope:           inScope,
				AssetType:         t.Kind,
				PlatformAssetType: "task_range",
				Identifier:        t.Value,
				BountyEligible:    inScope && bounty,
			})
		}
		if excludedPart != "" {
			for _, t := range scope.Targets(scope.AssetOther, separateNonASCII(excludedPart)) {
				out = append(out, platform.ScopeEntry{
					InScope:           false,
					AssetType:         t.Kind,
					PlatformAssetType: "task_range",
					Identifier:        t.Value,
					BountyEligible:    false,
				})
			}
		}
	}
	return out
}

var (
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
	spacesRE     = regexp.MustCompile(`[ \t\r\n\f]+`)
)

// htmlToMarkdown converts VulBox HTML descriptions into clean Markdown.
func htmlToMarkdown(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var out strings.Builder
	var lists []string
	var hrefs []string
	pre, skip := 0, 0
	block := func(sep string) {
		if out.Len() > 0 {
			out.WriteString(sep)
		}
	}
	newline := func() {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
	}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		t := z.Token()
		start := tt == html.StartTagToken || tt == html.SelfClosingTagToken
		switch tt {
		case html.TextToken:
			if skip > 0 {
				continue
			}
			text := t.Data
			if pre == 0 {
				text = spacesRE.ReplaceAllString(text, " ")
				if strings.HasSuffix(out.String(), "\n") || out.Len() == 0 {
					text = strings.TrimLeft(text, " ")
				}
			}
			out.WriteString(text)
			continue
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
		default:
			continue
		}
		switch name := t.Data; name {
		case "script", "style":
			if start {
				skip++
			} else if skip > 0 {
				skip--
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			block("\n\n")
			if start {
				out.WriteString(strings.Repeat("#", int(name[1]-'0')) + " ")
			}
		case "p", "div", "table", "blockquote", "section":
			block("\n\n")
		case "tr", "br":
			block("\n")
		case "td", "th":
			if !start {
				out.WriteString(" | ")
			}
		case "hr":
			block("\n\n---\n\n")
		case "ul", "ol":
			if start {
				if len(lists) == 0 {
					block("\n\n")
				} else {
					newline()
				}
				lists = append(lists, map[string]string{"ul": "*", "ol": "1."}[name])
			} else if len(lists) > 0 {
				if lists = lists[:len(lists)-1]; len(lists) == 0 {
					block("\n\n")
				}
			}
		case "li":
			if start {
				newline()
				marker := "*"
				if len(lists) > 0 {
					marker = lists[len(lists)-1]
				}
				out.WriteString(strings.Repeat("  ", max(0, len(lists)-1)) + marker + " ")
			}
		case "pre":
			if start {
				pre++
				block("\n\n")
				out.WriteString("```\n")
			} else if pre > 0 {
				pre--
				out.WriteString("\n```\n\n")
			}
		case "code":
			if pre == 0 {
				out.WriteString("`")
			}
		case "strong", "b":
			out.WriteString("**")
		case "em", "i":
			out.WriteString("*")
		case "a":
			if start {
				href := ""
				for _, a := range t.Attr {
					if a.Key == "href" && (strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "mailto:")) {
						href = a.Val
					}
				}
				hrefs = append(hrefs, href)
				if href != "" {
					out.WriteString("[")
				}
			} else if len(hrefs) > 0 {
				if href := hrefs[len(hrefs)-1]; href != "" {
					out.WriteString("](" + href + ")")
				}
				hrefs = hrefs[:len(hrefs)-1]
			}
		}
	}
	lines := strings.Split(out.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimSpace(blankLinesRE.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
