package store

import (
	"context"
	"errors"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestMailSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Acme Corp":                      "acmecorp",
		"acme-labs":                      "acmelabs",
		"Ünïcode Bug Bounty!":            "ncodebugbounty",
		"A very long program name, yes!": "averylongprogramname",
		"X":                              "program",
		"日本":                             "program",
	} {
		if got := MailSlug(in); got != want {
			t.Errorf("MailSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Each program gets one prefix, numbered when taken; addresses are the
// user's, unique, and only a program's own can be changed or forgotten.
func TestMailAddresses(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, nil)
	var acme, acme2 int64
	if err := st.Write(ctx, func(tx *Tx) error {
		var err error
		if acme, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"}); err != nil {
			return err
		}
		acme2, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme-2", Name: "ACME", URL: "https://example.com/acme2", State: "open"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if p, err := st.MailPrefixOf(ctx, acme); err != nil || p != "" {
		t.Fatalf("no prefix yet: %q %v", p, err)
	}
	p1, err := st.EnsureMailPrefix(ctx, acme, "acme")
	if err != nil || p1 != "acme" {
		t.Fatalf("first prefix: %q %v", p1, err)
	}
	if again, _ := st.EnsureMailPrefix(ctx, acme, "other"); again != "acme" {
		t.Errorf("a program keeps its prefix: %q", again)
	}
	if p2, _ := st.EnsureMailPrefix(ctx, acme2, "acme"); p2 != "acme2" {
		t.Errorf("a taken prefix gets a number: %q", p2)
	}
	ps, err := st.MailPrefixes(ctx)
	if err != nil || len(ps) != 2 || ps[0].Prefix != "acme2" || ps[0].Handle != "acme-2" {
		t.Errorf("prefixes, longest first: %+v %v", ps, err)
	}

	a, err := st.AddMailAddress(ctx, acme, " Acme-1731942000-K7Q2X@example.com ", "  second   account ")
	if err != nil || a.Address != "acme-1731942000-k7q2x@example.com" || a.Note != "second account" || a.ID == 0 {
		t.Fatalf("add: %+v %v", a, err)
	}
	if _, err := st.AddMailAddress(ctx, acme2, "acme-1731942000-k7q2x@example.com", ""); !errors.Is(err, ErrMailAddressTaken) {
		t.Errorf("a saved address again: %v", err)
	}
	if err := st.SetMailAddressNote(ctx, acme2, a.ID, "not mine"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another program's address: %v", err)
	}
	if err := st.SetMailAddressNote(ctx, acme, a.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	as, _ := st.MailAddresses(ctx, acme)
	if len(as) != 1 || as[0].Note != "admin" {
		t.Errorf("addresses: %+v", as)
	}
	if err := st.DeleteMailAddress(ctx, acme2, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("forget another program's address: %v", err)
	}
	if err := st.DeleteMailAddress(ctx, acme, a.ID); err != nil {
		t.Fatal(err)
	}
	if as, _ := st.MailAddresses(ctx, acme); len(as) != 0 {
		t.Errorf("forgotten: %+v", as)
	}
}
