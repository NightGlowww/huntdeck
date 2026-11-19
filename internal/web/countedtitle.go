package web

import (
	"strconv"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/store"
)

// countedTitleLang explains a 90-day figure in the page's language: the
// English is written by countedTitle (programcells.go) from the numbers; a
// Chinese page reads the same facts here.
func countedTitleLang(lang string, m *i18n.Bundle, p store.ProgramListItem, what string) string {
	if lang != "zh" {
		return countedTitle(p, what)
	}
	if p.CountedDays == nil {
		return ""
	}
	t := func(msg string) string { return m.T(lang, msg) }
	tf := func(msg string, args ...any) string { return m.TF(lang, msg, args...) }
	label := func() string {
		if what == "bounties paid" {
			return t("已付赏金")
		}
		return t("收到报告")
	}
	name, d := platformLabel(p.Platform), *p.CountedDays
	days := func(n int) string { return tf("%s 天", strconv.Itoa(n)) }
	const later = "保存满 90 天累计值后即为精确统计；见评分方法。"
	if p.Platform == "bugcrowd" {
		window := t("已保存的 90 天计数")
		if d < 90 {
			window = tf("已保存的 %s 计数（折算到 90 天）", days(d))
		}
		avg := ""
		if p.AvgPayout != nil {
			avg = "（" + humanize.MoneyFull(*p.AvgPayout, p.StatsCurrency) + "）"
		}
		return tf("估算：Bugcrowd 已奖励报告数在%s内的增量 × 其近 3 个月平均支出%s。该项目未在 CrowdStream 上列出报告；见评分方法。", window, avg)
	}
	weekly := what == "reports received" && p.Reports7d != nil
	switch {
	case d >= 90:
		return tf("统计：近 90 天的%s，由 %s 的累计总额逐日保存推算。", label(), name)
	case p.Points90d != nil && p.PointsAll != nil && *p.PointsAll > 0:
		total := ""
		switch {
		case what == "reports received" && p.TotalReports != nil:
			total = "（" + formatCount(float64(*p.TotalReports), "") + "）"
		case what == "bounties paid" && p.PaidAll != nil:
			total = "（" + humanize.MoneyFull(*p.PaidAll, p.StatsCurrency) + "）"
		}
		share := float64(*p.Points90d) / float64(*p.PointsAll) * 100
		return tf("估算：%s 的累计总额%s × 项目近 90 天排行榜积分占比：%s / %s，%.0f%%%s。积分来自被接受的报告及其重复，按危害计算。%s",
			name, total, formatCount(float64(*p.Points90d), ""), formatCount(float64(*p.PointsAll), ""), share, hunters90PhraseLang(m, lang, p.Hunters90d), later)
	case weekly && d == 0:
		return tf("估算：%s 近 7 天计数（%d）× 90/7。%s", name, *p.Reports7d, later)
	case weekly:
		return tf("估算：已保存 %s 内的%s，其余 %s 用 %s 近 7 天计数（%d）折算。%s",
			days(d), label(), days(90-d), name, *p.Reports7d, later)
	}
	return tf("估算：已保存 %s 内的%s，折算到 90 天。%s", days(d), label(), later)
}

// hunters90PhraseLang says how many researchers earned points lately.
func hunters90PhraseLang(m *i18n.Bundle, lang string, n *int) string {
	if n == nil || *n == 0 {
		return ""
	}
	return m.TF(lang, "（%d 位研究者）", *n)
}
