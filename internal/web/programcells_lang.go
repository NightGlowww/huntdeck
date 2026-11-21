package web

import (
	"fmt"
	"math"
	"strings"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/store"
)

// These wrappers keep the compact cells' tooltips in the same language as the
// page. The English helpers remain the source of truth for their calculations
// and for tests that exercise the default language.
func responseLang(lang string, messages *i18n.Bundle, p store.ProgramListItem) *responseTime {
	r := response(p)
	if r == nil {
		return nil
	}
	key := "First response: %s"
	if p.FirstResponseHours == nil {
		key = "To validate: %s"
	}
	return &responseTime{Text: r.Text, Title: messages.TF(lang, key, r.Text)}
}

func avgBountyTitleLang(lang string, messages *i18n.Bundle, p store.ProgramListItem) string {
	if p.AvgPayout == nil {
		return ""
	}
	v := amountRangeFull(p.AvgBountyLow, p.AvgPayout, p.StatsCurrency)
	if p.AvgBountyLow != nil && *p.AvgBountyLow == 0 {
		v = messages.TF(lang, "up to %s", humanize.MoneyFull(*p.AvgPayout, p.StatsCurrency))
	}
	key := "Average bounty: %s"
	switch p.Platform {
	case "hackerone":
		key = "HackerOne's average bounty: %s"
	case "bugcrowd":
		key = "Bugcrowd's average payout over the last 3 months: %s"
	case "yeswehack":
		key = "YesWeHack's average reward: %s"
	case "intigriti":
		key = "Intigriti's average payout: %s"
	}
	return messages.TF(lang, key, v)
}

func huntersLabelLang(lang string, messages *i18n.Bundle, platform string) string {
	key := "Hunters"
	switch platform {
	case "bugcrowd":
		key = "Hunters joined"
	case "yeswehack":
		key = "Hunters ranked"
	case "intigriti":
		key = "Hunters contributed"
	}
	return messages.T(lang, key)
}

func huntersHintLang(lang string, messages *i18n.Bundle, platform string) string {
	key := "Researchers on the program"
	switch platform {
	case "bugcrowd":
		key = "Researchers who joined the program"
	case "yeswehack":
		key = "Researchers on the program's ranking: those who scored on it"
	case "intigriti":
		key = "Researchers who contributed a report the program accepted"
	}
	return messages.T(lang, key)
}

func validTitleLang(lang string, messages *i18n.Bundle, platform string) string {
	key := "Valid reports in the last 90 days"
	switch platform {
	case "hackerone":
		key = "Valid reports in the last 90 days: reports resolved on HackerOne Hacktivity"
	case "bugcrowd":
		key = "Valid reports in the last 90 days: accepted reports resolved on Bugcrowd CrowdStream"
	}
	return messages.T(lang, key)
}

func validEstimateTitleLang(lang string, messages *i18n.Bundle, p store.ProgramListItem) string {
	if p.Reports90d == nil || p.AcceptedAll == nil || p.TotalReports == nil || *p.TotalReports == 0 {
		return ""
	}
	share := float64(*p.AcceptedAll) / float64(*p.TotalReports) * 100
	return messages.TF(lang, "Estimate of valid reports in 90 days: %s reports received in 90 days × %s%% of its reports accepted all time (%s of %s). %s doesn't list its reports, so none are counted as resolved; see Methodology.",
		num(*p.Reports90d), formatCount(math.Round(share), ""), num(*p.AcceptedAll), num(*p.TotalReports), platformLabel(p.Platform))
}

func assetsTitleLang(lang string, messages *i18n.Bundle, p store.ProgramListItem) string {
	n := func(v int, one, many string) string {
		if v == 1 {
			return "1 " + messages.T(lang, one)
		}
		return thousands(num(v)) + " " + messages.T(lang, many)
	}
	if p.InScope == 0 {
		return messages.T(lang, "No assets in scope")
	}
	out := n(p.InScope, "in-scope entry", "in-scope entries")
	var parts []string
	if p.Apps > 0 {
		parts = append(parts, n(p.Apps, "app", "apps"))
	}
	if p.APIs > 0 {
		parts = append(parts, n(p.APIs, "API", "APIs"))
	}
	if len(parts) > 0 {
		if rest := p.InScope - p.Apps - p.APIs; rest > 0 {
			parts = append(parts, thousands(num(rest))+" "+messages.T(lang, "other"))
		}
	}
	if len(parts) > 0 {
		out += ": " + joinLocalized(lang, messages, parts)
	}
	var hosts []string
	if p.Wildcards > 0 {
		hosts = append(hosts, n(p.Wildcards, "wildcard", "wildcards"))
	}
	if p.Domains > 0 {
		hosts = append(hosts, n(p.Domains, "domain", "domains"))
	}
	if len(hosts) > 0 {
		verb := messages.T(lang, "they name")
		if p.InScope == 1 {
			verb = messages.T(lang, "it names")
		}
		out += "; " + verb + " " + joinLocalized(lang, messages, hosts)
	}
	return out
}

func joinLocalized(lang string, messages *i18n.Bundle, values []string) string {
	if len(values) < 2 {
		return stringsJoin(values, "")
	}
	if len(values) == 2 {
		return values[0] + " " + messages.T(lang, "and") + " " + values[1]
	}
	return stringsJoin(values[:len(values)-1], ", ") + ", " + messages.T(lang, "and") + " " + values[len(values)-1]
}

func stringsJoin(values []string, sep string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += sep + value
	}
	return out
}

func chartLabelLang(lang string, messages *i18n.Bundle, label string) string {
	return messages.T(lang, label)
}

func scopeSameLang(lang string, messages *i18n.Bundle, same string) string {
	if lang != i18n.Chinese {
		return same
	}
	switch {
	case same == "All bounty-eligible":
		return messages.T(lang, same)
	case same == "No bounties":
		return messages.T(lang, same)
	case strings.HasPrefix(same, "All bounty-eligible, up to "):
		return messages.TF(lang, "All bounty-eligible, up to %s", strings.TrimPrefix(same, "All bounty-eligible, up to "))
	case strings.HasPrefix(same, "Up to "):
		return messages.TF(lang, "Up to %s", strings.TrimPrefix(same, "Up to "))
	default:
		return messages.T(lang, same)
	}
}

func trendHeadingLang(lang string, messages *i18n.Bundle, heading string) string {
	return chartLabelLang(lang, messages, heading)
}

func trendSummaryLang(lang string, messages *i18n.Bundle, heading, summary string) string {
	if lang != i18n.Chinese || summary == "" {
		return summary
	}
	if i := strings.Index(summary, " now, "); i >= 0 {
		value := summary[:i]
		rest := summary[i+6:]
		if j := strings.Index(rest, " on "); j >= 0 {
			change := rest[:j]
			when := rest[j+4:]
			if k := strings.LastIndex(when, " ("); k >= 0 && strings.HasSuffix(when, " then)") {
				base := strings.TrimSuffix(strings.TrimPrefix(when[k:], " ("), " then)")
				when = when[:k]
				return fmt.Sprintf("当前 %s，变化 %s（%s，当时 %s）", trendValueLang(value), change, dateLang(when), base)
			}
			return fmt.Sprintf("当前 %s，变化 %s（%s）", trendValueLang(value), change, dateLang(when))
		}
	}
	return translateChartText(lang, messages, summary)
}

func trendValueLang(value string) string {
	for _, pair := range []struct{ from, to string }{
		{" reports received in 90 days", " 近 90 天收到报告"},
		{" paid in 90 days (estimate)", " 近 90 天支付赏金（估算）"},
		{" paid in 90 days", " 近 90 天支付赏金"},
		{" resolved in 90 days", " 近 90 天解决报告"},
		{" valid in 90 days (estimate)", " 近 90 天有效报告（估算）"},
	} {
		if strings.HasSuffix(value, pair.from) {
			return strings.TrimSuffix(value, pair.from) + pair.to
		}
	}
	return value
}

func dateLang(value string) string {
	for _, pair := range []struct{ from, to string }{
		{"Jan ", "1 月 "}, {"Feb ", "2 月 "}, {"Mar ", "3 月 "}, {"Apr ", "4 月 "},
		{"May ", "5 月 "}, {"Jun ", "6 月 "}, {"Jul ", "7 月 "}, {"Aug ", "8 月 "},
		{"Sep ", "9 月 "}, {"Oct ", "10 月 "}, {"Nov ", "11 月 "}, {"Dec ", "12 月 "},
	} {
		if strings.HasPrefix(value, pair.from) {
			return pair.to + strings.TrimPrefix(value, pair.from)
		}
	}
	return value
}

func translateChartText(lang string, messages *i18n.Bundle, value string) string {
	if lang != i18n.Chinese {
		return value
	}
	for _, pair := range []struct{ from, to string }{
		{"Bounties paid · 90 days, estimated", "已付赏金 · 90 天（估算）"},
		{"Bounties paid · 90 days", "已付赏金 · 90 天"},
		{"Reports received · 90 days", "收到报告 · 90 天"},
		{"Valid reports · 90 days, estimated", "有效报告 · 90 天（估算）"},
		{"Valid reports · 90 days", "有效报告 · 90 天"},
		{"reports received in 90 days", "近 90 天收到报告"},
		{"paid in 90 days", "近 90 天支付赏金"},
		{"resolved in 90 days", "近 90 天解决报告"},
		{"valid in 90 days", "近 90 天有效报告"},
		{"none resolved", "无已解决"},
		{"bounties × the average", "份赏金 × 平均值"},
		{"median to resolve", "解决时间中位数"},
		{"no data", "暂无数据"},
		{"submitted", "已提交"},
		{"accepted", "已接收"},
		{"resolved", "已解决"},
		{"shown", "已展示"},
		{"so far", "截至目前"},
		{"saved ", "已保存 "},
		{"HuntDeck saves it daily: its line starts with the next day's", "HuntDeck 每天保存，曲线从次日的"},
	} {
		value = strings.ReplaceAll(value, pair.from, pair.to)
	}
	for _, pair := range []struct{ from, to string }{
		{"Jan ", "1 月 "}, {"Feb ", "2 月 "}, {"Mar ", "3 月 "}, {"Apr ", "4 月 "},
		{"May ", "5 月 "}, {"Jun ", "6 月 "}, {"Jul ", "7 月 "}, {"Aug ", "8 月 "},
		{"Sep ", "9 月 "}, {"Oct ", "10 月 "}, {"Nov ", "11 月 "}, {"Dec ", "12 月 "},
	} {
		value = strings.ReplaceAll(value, pair.from, pair.to)
	}
	return value
}

func chartDaysLang(lang string, messages *i18n.Bundle, days string) string {
	if lang != i18n.Chinese {
		return days
	}
	parts := strings.Split(days, ";")
	for i, part := range parts {
		fields := strings.SplitN(part, "|", 3)
		if len(fields) != 3 {
			continue
		}
		value := fields[2]
		value = translateChartText(lang, messages, value)
		parts[i] = strings.Join([]string{fields[0], fields[1], value}, "|")
	}
	return strings.Join(parts, ";")
}
