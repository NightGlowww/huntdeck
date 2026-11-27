package humanize

import (
	"testing"
	"time"
)

func TestMoney(t *testing.T) {
	tests := []struct {
		amount   float64
		currency string
		want     string
	}{
		{0, "USD", "$0"},
		{450, "USD", "$450"},
		{999.4, "", "$999"},
		{999.5, "USD", "$1k"},
		{1000, "USD", "$1k"},
		{1521, "USD", "$1.5k"},
		{8250, "USD", "$8.3k"},
		{9949, "USD", "$9.9k"},
		{9950, "USD", "$10k"},
		{10_200, "USD", "$10k"},
		{193_800, "USD", "$194k"},
		{999_499, "USD", "$999k"},
		{999_500, "USD", "$1M"},
		{1_400_000, "USD", "$1.4M"},
		{10_240_000, "USD", "$10.2M"},
		{41_000, "EUR", "€41k"},
		{750, "GBP", "£750"},
		{5000, "CNY", "¥5k"},
		{8250, "RMB", "¥8.3k"},
		{100_000, "JPY", "¥100k"},
		{41_000, "CHF", "41k CHF"},
		{-1200, "USD", "-$1.2k"},
	}
	for _, tt := range tests {
		if got := Money(tt.amount, tt.currency); got != tt.want {
			t.Errorf("Money(%v, %q) = %q, want %q", tt.amount, tt.currency, got, tt.want)
		}
	}
}

func TestMoneyFull(t *testing.T) {
	tests := []struct {
		amount   float64
		currency string
		want     string
	}{
		{0, "USD", "$0"},
		{8250, "USD", "$8,250"},
		{1_234_567.6, "EUR", "€1,234,568"},
		{10_000, "CNY", "¥10,000"},
		{50_000, "JPY", "¥50,000"},
		{500, "CHF", "500 CHF"},
	}
	for _, tt := range tests {
		if got := MoneyFull(tt.amount, tt.currency); got != tt.want {
			t.Errorf("MoneyFull(%v, %q) = %q, want %q", tt.amount, tt.currency, got, tt.want)
		}
	}
}

func TestMoneyRange(t *testing.T) {
	tests := []struct {
		lo, hi   float64
		want     string
		wantFull string
		currency string
	}{
		{5000, 25000, "$5k – $25k", "$5,000 – $25,000", "USD"},
		{500, 500, "$500", "$500", "USD"},
		{100, 12_500, "€100 – €13k", "€100 – €12,500", "EUR"},
		{100, 10_000, "¥100 – ¥10k", "¥100 – ¥10,000", "CNY"},
		{10_000, 300_000, "¥10k – ¥300k", "¥10,000 – ¥300,000", "JPY"},
		{500, 5000, "500 CHF – 5k CHF", "500 CHF – 5,000 CHF", "CHF"},
		// Compact ends that read the same are given in full.
		{5000, 5040, "$5,000 – $5,040", "$5,000 – $5,040", "USD"},
	}
	for _, tt := range tests {
		if got := MoneyRange(tt.lo, tt.hi, tt.currency); got != tt.want {
			t.Errorf("MoneyRange(%v, %v) = %q, want %q", tt.lo, tt.hi, got, tt.want)
		}
		if got := MoneyRangeFull(tt.lo, tt.hi, tt.currency); got != tt.wantFull {
			t.Errorf("MoneyRangeFull(%v, %v) = %q, want %q", tt.lo, tt.hi, got, tt.wantFull)
		}
	}
}

func TestDuration(t *testing.T) {
	const day = 24 * time.Hour
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{400 * time.Millisecond, "0s"},
		{13 * time.Second, "13s"},
		{59*time.Second + 600*time.Millisecond, "1m"},
		{2*time.Minute + 5*time.Second, "2m 5s"},
		{45 * time.Minute, "45m"},
		{3 * time.Hour, "3h"},
		{5*time.Hour + 20*time.Minute, "5h 20m"},
		{23*time.Hour + 59*time.Minute + 40*time.Second, "1d"},
		{7*day + 18*time.Hour, "7d 18h"},
		{16*day + 17*time.Hour, "16d 17h"},
		{29*day + 23*time.Hour + 40*time.Minute, "30d"},
		{51 * day, "51d"},
		{132*day + 7*time.Hour, "132d"},
		{181*day + 15*time.Hour, "6mo"},
		{251 * day, "8mo 1w"},
		{365 * day, "1y"},
		{2*365*day + 92*day, "2y 3mo"},
		{-3 * time.Hour, "3h"},
	}
	for _, tt := range tests {
		if got := Duration(tt.in); got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHoursAndDays(t *testing.T) {
	tests := []struct {
		hours float64
		want  string
	}{
		{0.4, "24m"},
		{5, "5h"},
		{186, "7d 18h"},
		{401, "16d 17h"},
		{3175.2, "132d"},
		{6 * 30.44 * 24, "6mo"},
	}
	for _, tt := range tests {
		if got := Hours(tt.hours); got != tt.want {
			t.Errorf("Hours(%v) = %q, want %q", tt.hours, got, tt.want)
		}
	}
	if got := Days(132.3); got != "132d" {
		t.Errorf("Days(132.3) = %q, want 132d", got)
	}
	if got := Days(3.5); got != "3d 12h" {
		t.Errorf("Days(3.5) = %q, want 3d 12h", got)
	}
}
