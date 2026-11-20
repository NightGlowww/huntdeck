package policycheck

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/mdpsec/huntdeck/internal/jev"
)

// A flagged rule is more useful with the sentence it came from: "breaks scope"
// is a verdict, "breaks scope, and here is the line" is something to read.
//
// Measured on two policies: asked to pick the sentence that states a rule out of
// three offered candidates, the model chose the right one at 0.99 (Phabricator's
// scope line) and 0.77 (HackerOne's header requirement). The candidates are
// chosen deterministically first - the choice is over a short list, not the whole
// policy - so the call stays small and the retrieval stays testable.
const (
	citeFloor      = 0.55
	citeCandidates = 3
)

// Sentences returns the policy's sentences of at least 20 characters, in order: a hard rule like
// "Do not test evil.example.com" is short, and a higher floor would drop it. It is
// a scanner and not a regexp because Go's RE2 has no lookbehind: "[.!?] followed
// by a space" cannot be written as one pattern.
func Sentences(policy string) []string {
	var out []string
	keep := func(s string) {
		// Markdown emphasis around a rule - *DO NOT TEST* - is noise in a
		// quotation: the sentence is what gets shown back to a person.
		s = strings.NewReplacer("*", "", "`", "").Replace(s)
		s = strings.Join(strings.Fields(s), " ")
		if len(s) >= 20 {
			out = append(out, s)
		}
	}
	for _, line := range strings.Split(policy, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "*-#> \t")
		if line == "" {
			continue
		}
		runes := []rune(line)
		var b strings.Builder
		for i, r := range runes {
			b.WriteRune(r)
			if r != '.' && r != '!' && r != '?' {
				continue
			}
			// A sentence ends at the rune itself, when the line ends there or a
			// space follows: "example.com" has no space after the dot, so hosts
			// and versions stay whole.
			if i+1 == len(runes) || runes[i+1] == ' ' {
				keep(b.String())
				b.Reset()
			}
		}
		keep(b.String())
	}
	return out
}

// rankSentences orders the policy's sentences by how many of the rule's words
// they contain: a deterministic prefilter, so the model chooses from a short
// list rather than scanning the whole text.
func rankSentences(sentences []string, rule string) []string {
	words := ruleWords(rule)
	type scored struct {
		s string
		n int
	}
	all := make([]scored, 0, len(sentences))
	for _, s := range sentences {
		lower := strings.ToLower(s)
		n := 0
		for _, w := range words {
			if strings.Contains(lower, w) {
				n++
			}
		}
		all = append(all, scored{s, n})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].n > all[j].n })
	out := make([]string, 0, citeCandidates)
	for _, s := range all {
		if len(out) == citeCandidates {
			break
		}
		out = append(out, s.s)
	}
	return out
}

func ruleWords(rule string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range regexp.MustCompile(`[a-z]{4,}`).FindAllString(strings.ToLower(rule), -1) {
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// Cite asks which sentence states a rule, and keeps the answer only when the
// model was sure of it.
func Cite(ctx context.Context, c *jev.Client, policy, rule string) (string, float64, error) {
	candidates := rankSentences(Sentences(policy), rule)
	if len(candidates) == 0 {
		return "", 0, nil
	}
	labels := make([]string, 0, len(candidates)+1)
	for i := range candidates {
		labels = append(labels, "S"+string(rune('0'+i)))
	}
	labels = append(labels, "none")
	// The policy goes along with the candidates: without it the model compares
	// sentences to a rule label in a vacuum, and the measured confidence (0.77-
	// 0.99) came from seeing both.
	trimmed := policy
	if len(trimmed) > maxPolicy {
		trimmed = trimmed[:maxPolicy]
	}
	state := map[string]any{"rule": rule, "policy": trimmed}
	for i, s := range candidates {
		state["S"+string(rune('0'+i))] = s
	}
	v, err := c.Choice(ctx, state,
		"Which of these policy sentences states this rule? Answer its label, or none.",
		labels)
	if err != nil {
		return "", 0, err
	}
	if v.Confidence < citeFloor || v.Choice == "none" {
		return "", v.Confidence, nil
	}
	for i := range candidates {
		if v.Choice == "S"+string(rune('0'+i)) {
			return candidates[i], v.Confidence, nil
		}
	}
	return "", v.Confidence, nil
}
