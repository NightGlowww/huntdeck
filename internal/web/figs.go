package web

import (
	"regexp"
	"strconv"
	"strings"
)

// Figures a chart labels itself with — "Jan", "Jan 2", "9mo 1w" — are
// formatted in Go (linechart.go, humanize) for the SVG and its axis; on a
// Chinese page they read in Chinese here.
var (
	figMonthDay = regexp.MustCompile(`^([A-Z][a-z]{2}) (\d{1,2})$`)
	figDate     = regexp.MustCompile(`^([A-Z][a-z]{2}) (\d{1,2}), (\d{4})$`)
	figSpan     = regexp.MustCompile(`([0-9]+)(mo|y|w|d|h|m)\b`)
)

var figMonths = map[string]string{
	"Jan": "1月", "Feb": "2月", "Mar": "3月", "Apr": "4月", "May": "5月", "Jun": "6月",
	"Jul": "7月", "Aug": "8月", "Sep": "9月", "Oct": "10月", "Nov": "11月", "Dec": "12月",
}

var figUnits = map[string]string{"y": "年", "mo": "个月", "w": "周", "d": "天", "h": "小时", "m": "分"}

// figText rewrites a chart label for the page's language; anything it
// doesn't recognise stays as it is.
func figText(lang, s string) string {
	if lang != "zh" {
		return s
	}
	if m, ok := figMonths[strings.TrimSpace(s)]; ok {
		return m
	}
	if g := figDate.FindStringSubmatch(s); g != nil {
		return g[3] + "年" + figMonths[g[1]] + g[2] + "日"
	}
	if g := figMonthDay.FindStringSubmatch(s); g != nil {
		return figMonths[g[1]] + g[2] + "日"
	}
	if figSpan.MatchString(s) {
		return figSpan.ReplaceAllStringFunc(s, func(part string) string {
			g := figSpan.FindStringSubmatch(part)
			return g[1] + " " + figUnits[g[2]]
		})
	}
	if n, err := strconv.Atoi(s); err == nil {
		return strconv.Itoa(n)
	}
	return s
}
