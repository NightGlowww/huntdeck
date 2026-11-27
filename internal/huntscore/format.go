package huntscore

import (
	"math"
	"strconv"
	"time"
)

var partLabels = map[string]string{Fresh: "Fresh", Competition: "Competition", Payout: "Payout", Response: "Response"}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func ago(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case d < time.Hour:
		return "just now"
	case days < 1:
		return plural(int(d.Hours()), "hour", "hours") + " ago"
	case days < 60:
		return plural(days, "day", "days") + " ago"
	default:
		return plural(days/30, "month", "months") + " ago"
	}
}

func platformLabel(name string) string {
	switch name {
	case "hackerone":
		return "HackerOne"
	case "bugcrowd":
		return "Bugcrowd"
	case "intigriti":
		return "Intigriti"
	case "yeswehack":
		return "YesWeHack"
	case "vulbox":
		return "VulBox"
	}
	return name
}
