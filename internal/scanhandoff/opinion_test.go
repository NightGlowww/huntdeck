package scanhandoff

import (
	"context"
	"errors"
	"testing"
)

type stubOpinion struct {
	v     Verdict
	err   error
	asked *State
}

func (s *stubOpinion) Judge(_ context.Context, st State) (Verdict, error) {
	s.asked = &st
	return s.v, s.err
}

// The second opinion can only narrow what the deterministic checks allow: a
// block or a confirm stands the handoff down, and a model that cannot answer is
// not permission either. This pins all three without a network or a database.
func TestSecondOpinionCanOnlyNarrow(t *testing.T) {
	cases := []struct {
		name    string
		v       Verdict
		err     error
		wantErr bool
	}{
		{"proceed", Verdict{Choice: ActProceed, Confidence: 0.9}, nil, false},
		{"split is allowed through", Verdict{Choice: ActSplitSmaller, Confidence: 0.6}, nil, false},
		{"block stops it", Verdict{Choice: ActBlock, Confidence: 0.87}, nil, true},
		{"confirm first stops it", Verdict{Choice: ActConfirmFirst, Confidence: 0.7}, nil, true},
		// An unreachable model is not a judgement on the batch: the deterministic
		// checks still govern, so the batch goes through rather than the feature
		// becoming unusable whenever a third party is down.
		{"unreachable is not a verdict", Verdict{}, errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opinion := &stubOpinion{v: c.v, err: c.err}
			err := runOpinion(context.Background(), opinion, State{Tasks: 4, Targets: 8, PerTask: 2})
			if c.wantErr && err == nil {
				t.Fatalf("%s was allowed", c.v.Choice)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("%s was refused: %v", c.v.Choice, err)
			}
			if opinion.asked == nil {
				t.Error("the opinion was never asked")
			}
		})
	}
}
