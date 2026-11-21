package scanhandoff

import (
	"context"
	"testing"
)

// The page attached the model's second opinion and the command did not, so a
// batch the model would have blocked went out anyway. The rule is the page's:
// block and confirm stop the batch, everything else runs.
type fixedOpinion struct{ choice string }

func (f fixedOpinion) Judge(context.Context, State) (Verdict, error) {
	return Verdict{Choice: f.choice, Confidence: 0.9, Model: "fixed"}, nil
}

func TestASecondOpinionStopsTheBatch(t *testing.T) {
	cases := []struct {
		choice string
		run    bool
	}{
		{ActProceed, true},
		{ActSplitSmaller, true},
		{ActConfirmFirst, false},
		{ActBlock, false},
	}
	for _, c := range cases {
		err := runOpinion(context.Background(), fixedOpinion{choice: c.choice}, State{Targets: 2})
		if c.run && err != nil {
			t.Errorf("%s stopped the batch: %v", c.choice, err)
		}
		if !c.run && err == nil {
			t.Errorf("%s let the batch through", c.choice)
		}
	}
}
