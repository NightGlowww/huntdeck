package store

import (
	"testing"
)

// A yen bounty is not a dollar bounty: the queue compares programs across
// currencies, so the amount is converted before it is scored.
func TestToUSD(t *testing.T) {
	cases := []struct {
		amount   float64
		currency string
		want     float64
	}{
		{1000, "USD", 1000},
		{1000, "usd", 1000},
		{1000, " EUR ", 1080},
		{500000, "JPY", 3200},
		{0, "USD", 0},
		{-5, "EUR", 0},
		{1000, "", 1000},
		{1000, "XYZ", 1000},
	}
	for _, c := range cases {
		got := ToUSD(c.amount, c.currency)
		if diff := got - c.want; diff > 0.001 || diff < -0.001 {
			t.Errorf("ToUSD(%v, %q) = %v, want %v", c.amount, c.currency, got, c.want)
		}
	}
}
