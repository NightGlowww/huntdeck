package policycheck

import (
	"strings"
	"testing"
)

// The splitter and the ranking are deterministic, so they are tested without a
// model. The choice on top of them was measured separately: 0.99 for
// Phabricator's scope line, 0.77 for HackerOne's header requirement.
func TestSentencesKeepsWhatAPersonWouldPointAt(t *testing.T) {
	policy := "# Rules\n\n* *DO NOT TEST* secure.example.com.\n* Report issues to security@example.com.\nshort\n"
	got := Sentences(policy)
	if len(got) < 2 {
		t.Fatalf("only %d sentences: %q", len(got), got)
	}
	for _, s := range got {
		if len(s) < 20 {
			t.Errorf("kept a fragment: %q", s)
		}
		if strings.Contains(s, "*") {
			t.Errorf("kept the markdown marker: %q", s)
		}
	}
}

// The prefilter must put the sentence that shares the rule's words first: the
// model is asked to choose among three, and a wrong shortlist is unanswerable.
func TestRankSentencesPutsTheMatchingLineFirst(t *testing.T) {
	policy := "Vulnerabilities in Phabricator and Arcanist are in scope. " +
		"Security is serious business, just like Phabricator. " +
		"If you can find a security vulnerability in the project, we will reward you with cash."
	rule := "the requirement to stay inside the listed scope"
	got := rankSentences(Sentences(policy), rule)
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	if !contains(got[0], "in scope") {
		t.Fatalf("the scope line was not ranked first: %q", got[0])
	}
}

// A policy whose sentences share no words with the rule still yields candidates:
// the model gets three lines and the option to answer "none".
func TestRankSentencesNeverReturnsNothing(t *testing.T) {
	got := rankSentences(Sentences("One sentence about cookies. Another about logos."), "the ban on denial-of-service")
	if len(got) == 0 {
		t.Fatal("no candidates for an unrelated rule")
	}
}
