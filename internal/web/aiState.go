package web

import (
	"context"
	"strings"
)

// aiState is what Settings says about the optional model. A feature that is off
// by default has to say so somewhere a person looks, not only in doctor.
type aiState struct {
	On       bool
	Model    string
	Readings int
}

// aiStateFor reads the configuration and the cached readings.
func (s *server) aiStateFor(ctx context.Context) aiState {
	base, model := strings.TrimSpace(s.AIBaseURL), strings.TrimSpace(s.AIModel)
	st := aiState{On: base != "" && model != "", Model: model}
	if s.Store != nil {
		if n, err := s.Store.CountAIRules(ctx); err == nil {
			st.Readings = n
		}
	}
	return st
}
