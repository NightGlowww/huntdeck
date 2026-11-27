// Package humanize formats amounts of money and durations the one way
// HuntDeck shows them, on every page and in HuntScore's reasons.
//
// What people read is compact: "$450", "$8.3k", "$1.4M"; "45m", "7d 18h",
// "8mo 1w". The full amount ("$8,250", MoneyFull) is for tooltips only,
// and never for an estimate.
package humanize

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Money is an amount as pages show it: whole under 1,000 ("$450"), then
// thousands with one decimal under 10k ("$8.3k"), whole thousands
// ("$194k") and millions with one decimal ("$1.4M"). Dollars, euros and
// pounds take their sign; other currencies their code after: "41k CHF".
func Money(amount float64, currency string) string {
	if amount < 0 {
		return "-" + Money(-amount, currency)
	}
	switch {
	case amount < 999.5:
		return withCurrency(strconv.FormatFloat(math.Round(amount), 'f', 0, 64), currency)
	case amount < 9_950:
		return withCurrency(oneDecimal(amount/1e3)+"k", currency)
	case amount < 999_500:
		return withCurrency(strconv.FormatFloat(math.Round(amount/1e3), 'f', 0, 64)+"k", currency)
	}
	return withCurrency(oneDecimal(amount/1e6)+"M", currency)
}

// MoneyFull is an amount to the unit, "$8,250": for tooltips, beside the
// compact Money a page shows. An estimate keeps to Money even there.
func MoneyFull(amount float64, currency string) string {
	return withCurrency(Thousands(strconv.FormatFloat(math.Round(amount), 'f', 0, 64)), currency)
}

// MoneyRange is a range of amounts, compact: "$5k – $25k", or one amount
// when the ends match. Where the compact ends would read the same though
// they differ ("$5k – $5k" for $5,000 – $5,040), it gives them in full.
func MoneyRange(lo, hi float64, currency string) string {
	if lo == hi {
		return Money(hi, currency)
	}
	a, b := Money(lo, currency), Money(hi, currency)
	if a == b {
		return MoneyRangeFull(lo, hi, currency)
	}
	return a + " – " + b
}

// MoneyRangeFull is MoneyRange to the unit, for tooltips: "$5,000 –
// $25,000".
func MoneyRangeFull(lo, hi float64, currency string) string {
	if lo == hi {
		return MoneyFull(hi, currency)
	}
	return MoneyFull(lo, currency) + " – " + MoneyFull(hi, currency)
}

func withCurrency(s, currency string) string {
	switch currency {
	case "", "USD":
		return "$" + s
	case "EUR":
		return "€" + s
	case "GBP":
		return "£" + s
	case "CNY", "RMB", "JPY":
		return "¥" + s
	}
	return s + " " + currency
}

// oneDecimal is v to one decimal place, without a trailing ".0".
func oneDecimal(v float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64), ".0")
}

// Thousands adds comma separators to a string of digits, keeping any sign.
func Thousands(digits string) string {
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	b.WriteString(sign)
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

const (
	day       = 24 * time.Hour
	monthDays = 30.44  // an average month
	yearDays  = 365.25 // an average year
)

// Duration is a length of time, compact, in at most its two largest
// units and no decimals: "13s", "2m 5s", "45m", "3h", "5h 20m", "7d 18h",
// "51d", "132d", "8mo 1w", "2y 3mo". Under a month it gives days and
// hours, then days alone up to six months, then months and weeks, then
// years and months. A negative duration is given as its size.
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if r := d.Round(time.Second); r < time.Minute {
		return strconv.Itoa(int(r/time.Second)) + "s"
	}
	if r := d.Round(time.Second); r < time.Hour {
		return two(int(r/time.Minute), "m", int(r%time.Minute/time.Second), "s")
	}
	if r := d.Round(time.Minute); r < day {
		return two(int(r/time.Hour), "h", int(r%time.Hour/time.Minute), "m")
	}
	if r := d.Round(time.Hour); r < 30*day {
		return two(int(r/day), "d", int(r%day/time.Hour), "h")
	}
	if r := d.Round(day); r < 182*day {
		return strconv.Itoa(int(r/day)) + "d"
	}
	days := d.Hours() / 24
	if days < yearDays {
		m := int(days / monthDays)
		w := int(math.Round((days - float64(m)*monthDays) / 7))
		if w >= 4 { // four weeks make the next month, not "5mo 4w"
			m, w = m+1, 0
		}
		if m < 12 {
			return two(m, "mo", w, "w")
		}
	}
	y := int(days / yearDays)
	m := int(math.Round((days - float64(y)*yearDays) / monthDays))
	if m >= 12 {
		y, m = y+1, 0
	}
	return two(y, "y", m, "mo")
}

// Hours is Duration for a length given in hours, as platforms give
// response times: 186 → "7d 18h".
func Hours(h float64) string { return Duration(time.Duration(h * float64(time.Hour))) }

// Days is Duration for a length given in days, as medians to resolve are:
// 132.3 → "132d", 3.5 → "3d 12h".
func Days(d float64) string { return Hours(d * 24) }

// two is a count in a unit and the next one down, leaving out the second
// when it's zero: "7d 18h", "51d".
func two(a int, aUnit string, b int, bUnit string) string {
	if b == 0 {
		return strconv.Itoa(a) + aUnit
	}
	return strconv.Itoa(a) + aUnit + " " + strconv.Itoa(b) + bUnit
}
