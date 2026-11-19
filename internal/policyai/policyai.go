package policyai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/ai"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Options bound one run.
type Options struct {
	Platform string
	Limit    int  // how many programs to look at
	Force    bool // read again even when the policy has not changed
	PerItem  time.Duration
}

// Result is what a run did.
type Result struct {
	Read, Skipped, Failed int
	Errors                []string
}

// System is the instruction; the policy text follows as the user message.
const System = "You read bug bounty program policies and list the rules a tester must follow. " +
	"Answer with JSON only: an array of objects with title (one short sentence, imperative), " +
	"detail (optional, one sentence) and kind (one of header, email, rate, creds, warn, other). " +
	"Use only what the policy states; do not invent rules and do not restate the bounty table."

// Extract reads each policy the model has not seen and stores what it finds. The
// cache key is the policy's hash, so the same text is never sent twice.
func Extract(ctx context.Context, st *store.Store, client *ai.Client, o Options) (Result, error) {
	var res Result
	if !client.Configured() {
		return res, fmt.Errorf("no endpoint configured")
	}
	rows, err := st.PoliciesWithText(ctx, o.Platform, o.Limit)
	if err != nil {
		return res, err
	}
	per := o.PerItem
	if per <= 0 {
		per = 30 * time.Second
	}
	for _, p := range rows {
		policy := strings.TrimSpace(p.Policy)
		if len(policy) < 80 {
			res.Skipped++
			continue
		}
		hash := store.PolicyHash(policy)
		if !o.Force {
			if got, err := st.AIReading(ctx, p.ID, hash); err == nil && got != nil {
				res.Skipped++
				continue
			}
		}
		itemCtx, cancel := context.WithTimeout(ctx, per)
		var rules []store.AIRule
		err := client.CompleteJSON(itemCtx, System, userMessage(policy), &rules)
		cancel()
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s/%s: %v", p.Platform, p.Handle, err))
			continue
		}
		if len(rules) > 12 {
			rules = rules[:12]
		}
		if err := st.SaveAIRules(ctx, p.ID, hash, rules, client.Model); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s/%s: %v", p.Platform, p.Handle, err))
			continue
		}
		res.Read++
	}
	return res, nil
}

// userMessage keeps a policy under the model's context without cutting a
// sentence in half.
func userMessage(policy string) string {
	return "Policy text:\n\n" + trim(policy, 12000) + "\n\nList its rules."
}

func trim(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, ".\n"); i > max/2 {
		return cut[:i+1]
	}
	return cut
}
