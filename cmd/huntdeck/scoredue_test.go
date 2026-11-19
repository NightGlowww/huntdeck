package main

import (
	"testing"
	"time"
)

// The caller is the server's 30-minute ticker. This pins the comparison it
// makes, which was wrong once: a local date would ask for a row that already
// exists every evening east of Greenwich.
func TestScoreDayDue(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 07:00 on the 19th in Shanghai is 23:00Z on the 18th: today's UTC row is
	// the 18th, and it exists, so nothing is due.
	evening := time.Date(2026, 11, 19, 7, 0, 0, 0, shanghai)
	if scoreDayDue("2026-11-18", evening) {
		t.Error("the 18th exists at 23:00Z on the 18th: nothing is due")
	}
	// 08:00 on the 19th in Shanghai is 00:00Z on the 19th: the 19th is due.
	morning := time.Date(2026, 11, 19, 8, 0, 0, 0, shanghai)
	if !scoreDayDue("2026-11-18", morning) {
		t.Error("the UTC day rolled over: the 19th is due")
	}
	if scoreDayDue("2026-11-19", morning) {
		t.Error("the 19th already exists: nothing is due")
	}
}
