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
		err := runOpinion(context.Background(), fixedOpinion{choice: c.choice}, State{Targets: 2}, "")
		if c.run && err != nil {
			t.Errorf("%s stopped the batch: %v", c.choice, err)
		}
		if !c.run && err == nil {
			t.Errorf("%s let the batch through", c.choice)
		}
	}
}

// confirm_first asks for "someone should look". A named handoff is exactly
// that: the operator picked the targets, so the confirmation stands and the
// batch runs. Without it the verdict still stops the batch.
func TestConfirmFirstPassesWhenConfirmed(t *testing.T) {
	if err := runOpinion(context.Background(), fixedOpinion{choice: ActConfirmFirst}, State{Targets: 2}, "scan-handoff command"); err != nil {
		t.Fatalf("a confirmed batch was stopped: %v", err)
	}
	if err := runOpinion(context.Background(), fixedOpinion{choice: ActConfirmFirst}, State{Targets: 2}, ""); err == nil {
		t.Fatal("an unconfirmed confirm_first let the batch through")
	}
	if err := runOpinion(context.Background(), fixedOpinion{choice: ActBlock}, State{Targets: 2}, "scan-handoff command"); err == nil {
		t.Fatal("a block must stand even with a confirmation")
	}
}
