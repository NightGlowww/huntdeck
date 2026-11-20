package huntqueue

import "testing"

// A real row rendered a three-million-dollar top bounty as "$3000.0k": correct
// arithmetic, and a number a reader has to divide to understand. The queue now
// borrows humanize.Money's ladder like every other page, so the expectations
// below are that ladder's.
func TestMoneyTextLadder(t *testing.T) {
	cases := []struct {
		usd  float64
		want string
	}{
		{4.5, "5"},
		{450, "450"},
		{1_000, "1k"},
		{8_250, "8.3k"},
		{15_000, "15k"},
		{133_700, "134k"},
		{999_000, "999k"},
		{1_400_000, "1.4M"},
		{3_000_000, "3M"},
	}
	for _, c := range cases {
		if got := moneyText(c.usd); got != c.want {
			t.Errorf("moneyText(%v) = %q, want %q", c.usd, got, c.want)
		}
	}
}

// The sentence the page builds has no currency symbol of its own: the reason
// template supplies the "$", so the amount must not carry one.
func TestMoneyTextHasNoSign(t *testing.T) {
	for _, v := range []float64{5, 1_000, 3_000_000} {
		if got := moneyText(v); got != "" && got[0] == '$' {
			t.Errorf("moneyText(%v) = %q carries a currency sign", v, got)
		}
	}
}
