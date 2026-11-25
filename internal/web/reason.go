package web

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/mdpsec/huntdeck/internal/i18n"
)

// HuntScore reasons are formatted when a score is computed and stored with
// it, so the page translates the shapes scoring writes rather than the
// sentences it once wrote: TestReasonShapes has each of them.
//
// The comparison at the end ("top 12% on HackerOne", "busier than 93% of
// HackerOne programs") is marked while translating, then bolded.

type reasonShape struct {
	re *regexp.Regexp
	zh string // Chinese, %s per group
	// alt translates groups that are words, not numbers.
	alt func(g []string) []string
}

var reasonShapes = []reasonShape{
	{re: regexp.MustCompile(`^No new scope in the last 90 days$`), zh: "近 90 天没有新增范围"},
	{re: regexp.MustCompile(`^Running since (\d{4}): well picked over$`), zh: "自 %s 年运营至今，已被充分挖掘"},
	{re: regexp.MustCompile(`^acceptance speed not published: platform median used$`), zh: "未公开接受速度：用平台中位数"},
	{re: regexp.MustCompile(`^automation banned: hand testing only$`), zh: "禁止自动化：只能手工测试"},
	{re: regexp.MustCompile(`^credentials have to be requested$`), zh: "需要先申请测试凭据"},
	{re: regexp.MustCompile(`^nothing to arrange: start now$`), zh: "无需准备：现在就能开始"},
	{re: regexp.MustCompile(`^already handed to the scanner$`), zh: "已派发给扫描器，还没读到状态"},
	{re: regexp.MustCompile(`^already scanned: the scanner finished it$`), zh: "已扫完：扫描器完成了这个目标"},
	{re: regexp.MustCompile(`^the scanner failed on it: hand it over again$`), zh: "扫描器在这个目标上失败了：可重新派发"},
	{re: regexp.MustCompile(`^the scanner no longer has it: hand it over again$`), zh: "扫描器已没有这个任务：可重新派发"},
	{re: regexp.MustCompile(`^the scanner is on it now$`), zh: "扫描器正在处理"},
	{re: regexp.MustCompile(`^No bounties \(disclosure program\)$`), zh: "无赏金（仅披露项目）"},
	{re: regexp.MustCompile(`^(\d+)% on time$`), zh: "%s%% 按时"},
	{re: regexp.MustCompile(`^Most reports validated within (.+)$`), zh: "多数报告在 %s 内完成验证"},
	{re: regexp.MustCompile(`^First response in about (.+)$`), zh: "首次响应约 %s"},
	{re: regexp.MustCompile(`^Average bounty (\$[\d.,]+k?) \(top (\d+)% on ([\w.-]+)\)$`),
		zh: "平均赏金 %s（%s 前 %s%%）", alt: swap(1, 2)},
	{re: regexp.MustCompile(`^Bounties up to (\$[\d.,]+k?) \(top (\d+)% on ([\w.-]+)\)$`),
		zh: "最高赏金 %s（%s 前 %s%%）", alt: swap(1, 2)},
	{re: regexp.MustCompile(`^Top bounty (\$[\d.,]+k?) \(top (\d+)% on ([\w.-]+)\)$`),
		zh: "最高赏金 %s（%s 前 %s%%）", alt: swap(1, 2)},
	{re: regexp.MustCompile(`^(\$[\d.,]+k?) paid in the last (\d+) days \(top (\d+)% on ([\w.-]+)\)$`),
		zh: "%s 支出（近 %s 天，%s 前 %s%%）", alt: paidAlt},
	{re: regexp.MustCompile(`^(\d+) reports in (\d+) days for (\d+) in-scope assets: (busier|quieter) than (\d+)% of ([\w.-]+) programs$`),
		zh: "%s 天 %s 份报告 / %s 个范围内资产：比 %s 的 %s%% 项目更%s", alt: compareAlt},
	{re: regexp.MustCompile(`^(\d+) accepted reports in (\d+) days for (\d+) in-scope assets: (busier|quieter) than (\d+)% of ([\w.-]+) programs$`),
		zh: "%s 天 %s 份已接受报告 / %s 个范围内资产：比 %s 的 %s%% 项目更%s", alt: compareAlt},
	{re: regexp.MustCompile(`^≈(\d+) rewarded reports in (\d+) days \(from (\d+) days of saved counts\) for (\d+) in-scope assets: (busier|quieter) than (\d+)% of ([\w.-]+) programs$`),
		zh: "≈%s 份有赏金报告（%s 天，依据 %s 天已保存计数）/ %s 个范围内资产：比 %s 的 %s%% 项目更%s", alt: compareAltLong},
	{re: regexp.MustCompile(`^([\w.-]+) doesn't share report volume$`), zh: "%s 不公开报告量"},
	{re: regexp.MustCompile(`^([\w.-]+) doesn't share response times$`), zh: "%s 不公开响应时间"},
	{re: regexp.MustCompile(`^Private: invite-only, far fewer hunters$`), zh: "私有：仅限邀请，竞争者少得多"},
	{re: regexp.MustCompile(`^Launched (\d+) months ago: not picked over yet$`), zh: "%s 个月前上线：尚未被充分挖掘"},
	{re: regexp.MustCompile(`^Launched (.+) ago: not picked over yet$`), zh: "%s前上线：尚未被充分挖掘"},
	{re: regexp.MustCompile(`^Invited (.+) ago$`), zh: "%s前收到邀请", alt: durAlt},
	{re: regexp.MustCompile(`^Reopened (.+) ago$`), zh: "%s前重新开放", alt: durAlt},
	{re: regexp.MustCompile(`^Newest: (.+), (.+) ago$`), zh: "最新：%s（%s前）", alt: durAlt2},
	{re: regexp.MustCompile(`^(\d+) assets? added in the last 30 days: (.+)$`), zh: "近 30 天新增 %s 个资产：%s", alt: assetDetail},
	// A scored target's reasons (huntscore/targets.go).
	{re: regexp.MustCompile(`^(.+) added (.+) ago$`), zh: "%s 新增于 %s前", alt: kindAndDuration},
	{re: regexp.MustCompile(`^(.+) new to you: in scope when you joined (.+) ago$`), zh: "%s 对你而言是新的：加入时已在范围内（%s前）", alt: kindAndDuration},
	{re: regexp.MustCompile(`^Program pays no bounties$`), zh: "项目不付赏金"},
	{re: regexp.MustCompile(`^Not bounty-eligible$`), zh: "不可获赏金"},
	{re: regexp.MustCompile(`^Bounty-eligible up to (.+)$`), zh: "可获赏金，最高 %s", alt: severityAlt},
	{re: regexp.MustCompile(`^Bounty-eligible$`), zh: "可获赏金"},
	{re: regexp.MustCompile(`^Root domain new to the program$`), zh: "根域对该项目是新的"},
	{re: regexp.MustCompile(`^No other program lists it$`), zh: "没有其他项目列出它"},
	{re: regexp.MustCompile(`^Also listed by (\d+) other programs?$`), zh: "另有 %s 个项目也列出"},
	{re: regexp.MustCompile(`^Program HuntScore (\d+) \(competition (\d+), payout (\d+), response (\d+)\)$`),
		zh: "项目 HuntScore %s（竞争 %s，赏金 %s，响应 %s）"},
}

// swap reorders groups for a translation whose words come in another
// order: the comparisons read "top 12% on HackerOne" in English and
// "HackerOne 前 12%" in Chinese.
func swap(amount, rank int) func([]string) []string {
	return func(g []string) []string {
		if len(g) < 3 {
			return g
		}
		return []string{g[amount-1], g[2], g[rank-1]}
	}
}

// compareAlt handles "...: busier than 93% of HackerOne programs", whose
// words come in another order in Chinese.
func compareAlt(g []string) []string {
	if len(g) < 6 {
		return g
	}
	return []string{g[1], g[0], g[2], g[5], g[4], mood(g[3])}
}

// paidAlt reorders "\$54k paid ... (top 20% on HackerOne)".
func paidAlt(g []string) []string {
	if len(g) < 4 {
		return g
	}
	return []string{g[0], g[1], g[3], g[2]}
}

func compareAltLong(g []string) []string {
	if len(g) < 7 {
		return g
	}
	return []string{g[0], g[1], g[2], g[3], g[6], g[5], mood(g[4])}
}

// mood turns the comparison's direction into a word.
func mood(word string) string {
	if word == "quieter" {
		return "冷清"
	}
	return "活跃"
}

// kindAndDuration translates an asset kind and a relative time.
func kindAndDuration(g []string) []string {
	if len(g) < 2 {
		return g
	}
	return []string{kindZh(g[0]), durationText(g[1])}
}

// severityAlt translates a severity word.
func severityAlt(g []string) []string {
	if len(g) < 1 {
		return g
	}
	return []string{sevZh(g[0])}
}

var kindZhMap = map[string]string{
	"Domain": "域名", "Wildcard": "通配符", "URL": "URL", "IP": "IP", "Cidr": "网段",
	"Root domain": "根域名", "Android app": "安卓应用", "iOS app": "iOS 应用",
	"Hardware": "硬件", "Other": "其他", "Source code": "源码", "Smart contract": "智能合约",
}

func kindZh(k string) string {
	if v, ok := kindZhMap[k]; ok {
		return v
	}
	return k
}

var sevZhMap = map[string]string{"critical": "严重", "high": "高危", "medium": "中危", "low": "低危", "info": "信息"}

func sevZh(s string) string {
	if v, ok := sevZhMap[s]; ok {
		return v
	}
	return s
}

// durAlt and durAlt2 translate the duration groups of the relative shapes.
func durAlt(g []string) []string {
	if len(g) < 1 {
		return g
	}
	return []string{durationText(g[0])}
}

func durAlt2(g []string) []string {
	if len(g) < 2 {
		return g
	}
	return []string{g[0], durationText(g[1])}
}

// assetDetail translates the tail of "3 assets added ...: 1 new root
// domain, 2 wildcards" word by word.
func assetDetail(g []string) []string {
	if len(g) < 2 {
		return g
	}
	return []string{g[0], translateAssetWords(g[1])}
}

var assetWord = []struct {
	re *regexp.Regexp
	zh string
}{
	{regexp.MustCompile(`\bnew root domains?\b`), "新根域"},
	{regexp.MustCompile(`\bwildcards?\b`), "通配符"},
	{regexp.MustCompile(`\bbounty-eligible\b`), "可获赏金"},
	{regexp.MustCompile(`\bcritical\b`), "严重"},
	{regexp.MustCompile(`\bhigh\b`), "高危"},
	{regexp.MustCompile(`\bmedium\b`), "中危"},
}

func translateAssetWords(s string) string {
	for _, w := range assetWord {
		s = w.re.ReplaceAllString(s, w.zh)
	}
	return s
}

// durationZh turns "2 days" or "1d 17h" into Chinese where it can.
var durationZh = []struct {
	re *regexp.Regexp
	zh string
}{
	{regexp.MustCompile(`^(\d+) days?$`), "%s 天"},
	{regexp.MustCompile(`^(\d+) months?$`), "%s 个月"},
	{regexp.MustCompile(`^(\d+) hours?$`), "%s 小时"},
	{regexp.MustCompile(`^(\d+) years?$`), "%s 年"},
}

func durationText(s string) string {
	for _, d := range durationZh {
		if g := d.re.FindStringSubmatch(s); g != nil {
			return fmt.Sprintf(d.zh, g[1])
		}
	}
	return s
}

// reasonText rewrites a stored reason for the page's language; a shape it
// doesn't know stays English.
func reasonText(lang string, m *i18n.Bundle, s string) string {
	if lang != "zh" {
		return s
	}
	for _, sh := range reasonShapes {
		g := sh.re.FindStringSubmatch(s)
		if g == nil {
			continue
		}
		args := g[1:]
		if sh.alt != nil {
			args = sh.alt(args)
		}
		anyArgs := make([]any, len(args))
		for i, a := range args {
			anyArgs[i] = template.HTMLEscapeString(a) // assets and names come from platforms
		}
		return fmt.Sprintf(sh.zh, anyArgs...)
	}
	return s
}

// reasonHTML is a reason with its comparison in bold, escaped for the page.
func reasonHTML(lang string, m *i18n.Bundle, r string) template.HTML {
	if lang == "zh" {
		return template.HTML(reasonText(lang, m, r)) //nolint:gosec // every group is escaped in reasonText
	}
	esc := template.HTMLEscapeString(r)
	return template.HTML(huntComparison.ReplaceAllString(esc, "<b>$1</b>")) //nolint:gosec // escaped above
}

// reasonsText joins a target's stored reasons for a tooltip, each in the
// page's language.
func reasonsText(lang string, m *i18n.Bundle, rs []string) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = reasonText(lang, m, r)
	}
	return strings.Join(out, " · ")
}

// reqTitleShapes translate the requirement chips' tooltips, which are
// formatted when the chip is made (requirements.go).
var reqTitleShapes = []struct {
	re *regexp.Regexp
	zh string
}{
	{regexp.MustCompile(`^([\d.]+) or higher past-year Signal on (.+); below it you get trial reports only$`), "过去一年 Signal 需 ≥ %s（%s）；低于此仅有试用报告"},
	{regexp.MustCompile(`^Identity-verified hackers only \(ID verification on (.+)\)$`), "仅限通过身份验证的研究者（在 %s 完成身份验证）"},
	{regexp.MustCompile(`^Background-cleared hackers only$`), "仅限通过背景审查的研究者"},
	{regexp.MustCompile(`^Residents of the European Economic Area only$`), "仅限欧洲经济区居民"},
	{regexp.MustCompile(`^Researchers apply and must be accepted before testing$`), "研究者需申请并通过后才能测试"},
	{regexp.MustCompile(`^You must accept the program's own terms and conditions on (.+) first$`), "需先在 %s 接受该项目自己的条款"},
	{regexp.MustCompile(`^Your (.+) account must have two-factor authentication on$`), "你的 %s 账户必须开启两步验证"},
	{regexp.MustCompile(`^Test credentials available from the platform$`), "平台提供测试账号与凭据"},
}

// reqTitle rewrites a requirement tooltip for the page's language.
func reqTitle(lang, s string) string {
	if lang != "zh" {
		return s
	}
	for _, sh := range reqTitleShapes {
		if g := sh.re.FindStringSubmatch(s); g != nil {
			args := make([]any, len(g)-1)
			for i, a := range g[1:] {
				args[i] = template.HTMLEscapeString(a)
			}
			return fmt.Sprintf(sh.zh, args...)
		}
	}
	return s
}
