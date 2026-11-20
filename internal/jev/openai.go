package jev

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAI's Decisions API is the one provider whose body is not a table of
// questions: questions are an array with a name, noul is called predicate, and
// a choice's options and a score's levels are objects rather than strings.
//
//   POST /v1/decisions
//   {model, input (a string or messages), questions:[{type,name,instructions,
//    choices:[{value,description}] | levels:[{label,description}]}]}
//
// The answers come back as an array of objects that each carry their name, and a
// refusal is a type of its own - it must not be read as a zero probability.

type oaQuestion struct {
	Type         string     "json:\"type\""
	Name         string     "json:\"name\""
	Instructions string     "json:\"instructions\""
	Choices      []oaOption "json:\"choices,omitempty\""
	Levels       []oaOption "json:\"levels,omitempty\""
}

type oaOption struct {
	Value       string "json:\"value,omitempty\""
	Label       string "json:\"label,omitempty\""
	Description string "json:\"description,omitempty\""
}

type oaRequest struct {
	Model     string       "json:\"model\""
	Input     any          "json:\"input\""
	Questions []oaQuestion "json:\"questions\""
}

type oaProb struct {
	Value       string  "json:\"value\""
	Probability float64 "json:\"probability\""
}

type oaAnswer struct {
	Type          string   "json:\"type\""
	Name          string   "json:\"name\""
	Choice        string   "json:\"choice\""
	Score         float64  "json:\"score\""
	Probability   float64  "json:\"probability\""
	Confidence    float64  "json:\"confidence\""
	Probabilities []oaProb "json:\"probabilities\""
}

type oaResponse struct {
	Answers []oaAnswer "json:\"answers\""
	Error   *struct {
		Message string "json:\"message\""
	} "json:\"error\""
}

// toOpenAI translates the shared question table into their array.
func toOpenAI(model string, state any, questions map[string]Question) ([]byte, error) {
	out := oaRequest{Model: model, Input: openaiInput(state)}
	for id, q := range questions {
		oq := oaQuestion{Name: id, Instructions: q.Instructions}
		switch q.Type {
		case "noul":
			oq.Type = "predicate"
		case "choice":
			oq.Type = "choice"
			if m, ok := q.Criteria.(map[string]string); ok {
				for v, d := range m {
					oq.Choices = append(oq.Choices, oaOption{Value: v, Description: d})
				}
			}
		case "score":
			oq.Type = "score"
			if lv, ok := q.Criteria.([]string); ok {
				for _, l := range lv {
					oq.Levels = append(oq.Levels, oaOption{Label: labelOf(l), Description: l})
				}
			}
		default:
			return nil, fmt.Errorf("jev: openai does not take a %q question", q.Type)
		}
		out.Questions = append(out.Questions, oq)
	}
	return json.Marshal(out)
}

// openaiInput passes a string state through and wraps anything else as JSON
// evidence in a user message, the shape their guide shows.
func openaiInput(state any) any {
	if s, ok := state.(string); ok {
		return s
	}
	return []map[string]any{{"role": "user", "content": []map[string]any{
		{"type": "input_text", "text": "Evaluate the questions against this evidence."},
		{"type": "input_json", "data": state},
	}}}
}

// labelOf keeps a level's label short: the text before a colon when there is
// one, the line itself otherwise.
func labelOf(level string) string {
	if i := strings.Index(level, ":"); i > 0 && i < 40 {
		return strings.TrimSpace(level[:i])
	}
	if len(level) > 40 {
		return strings.TrimSpace(level[:40])
	}
	return level
}

// fromOpenAI turns their answer array into the map the rest of this package
// works in. A refusal is an error, not a zero.
func fromOpenAI(raw []byte) (map[string]Answer, error) {
	var resp oaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("jev: openai response: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return nil, fmt.Errorf("jev: %s", resp.Error.Message)
	}
	out := make(map[string]Answer, len(resp.Answers))
	for _, a := range resp.Answers {
		if a.Type == "refusal" {
			return nil, fmt.Errorf("jev: the model refused question %q", a.Name)
		}
		conv := Answer{Type: a.Type, Confidence: a.Confidence}
		switch a.Type {
		case "choice":
			conv.Choice = a.Choice
			conv.Probabilities = probs(a.Probabilities)
		case "score":
			conv.Score = a.Score
			conv.Probabilities = probs(a.Probabilities)
		case "predicate":
			conv.Noul = a.Probability
		}
		out[a.Name] = conv
	}
	return out, nil
}

func probs(in []oaProb) map[string]float64 {
	out := make(map[string]float64, len(in))
	for _, p := range in {
		out[p.Value] = p.Probability
	}
	return out
}
