package web

import (
	"slices"
	"testing"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Mail a platform relays names its program by the sender's name: the
// start of one of that platform's programs' names or handles, word by
// word.
func TestRelayedFor(t *testing.T) {
	progs := []store.ProgramRef{
		{ID: 1, Platform: "hackerone", Handle: "acmes_general_store", Name: "Acme's General Store"},
		{ID: 2, Platform: "bugcrowd", Handle: "acmes-bc", Name: "Acme's General Store"},
		{ID: 3, Platform: "hackerone", Handle: "acmes_cloud", Name: "Acme's Cloud", Private: true},
		{ID: 4, Platform: "hackerone", Handle: "acmesoft", Name: "Acmesoft"},
		{ID: 5, Platform: "hackerone", Handle: "hackerone", Name: "HackerOne"},
		{ID: 6, Platform: "hackerone", Handle: "security_corp", Name: "Security Corp"},
	}
	named := map[string][]store.ProgramRef{}
	for _, p := range progs {
		for _, k := range []string{store.MailSlug(p.Name), store.MailSlug(p.Handle)} {
			named[k] = append(named[k], p)
		}
	}
	for _, tt := range []struct {
		from, platform string
		want           []int64
	}{
		{"Acme's <mail-forwarder@wearehackerone.com>", "hackerone", []int64{3, 1}}, // private first; not Acmesoft, not Bugcrowd's
		{"Acme’s General Store <mail-forwarder@wearehackerone.com>", "hackerone", []int64{1}},
		{"acmes cloud <x@wearehackerone.com>", "hackerone", []int64{3}},
		{"Acme's <x@bugcrowdninja.com>", "bugcrowd", []int64{2}},
		{"Acmesoft Team <x@wearehackerone.com>", "hackerone", nil}, // more words than the name has
		{"HackerOne <x@wearehackerone.com>", "hackerone", nil},     // the platform itself
		{"Security <x@wearehackerone.com>", "hackerone", nil},      // too common a word
		{"Acm <x@wearehackerone.com>", "hackerone", nil},           // too short
		{"<x@wearehackerone.com>", "hackerone", nil},
		{"not an address", "hackerone", nil},
	} {
		got, _ := relayedFor(huntmail.Email{HeaderFrom: tt.from}, tt.platform, named)
		var ids []int64
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		if !slices.Equal(ids, tt.want) {
			t.Errorf("%q on %s: %v, want %v", tt.from, tt.platform, ids, tt.want)
		}
	}
}

// Bugcrowd's relay names the real sender at the start of the subject.
func TestRelayedApex(t *testing.T) {
	for subject, want := range map[string]string{
		"[Relayed on behalf of no-reply@mail.acme.example to me@bugcrowdninja.com] Your account": "acme.example",
		"[Relayed on behalf of a@Shop.Example.co.uk to x@bugcrowdninja.com] Hi":                  "example.co.uk",
		"  [Relayed on behalf of a@acme.example. to x@bugcrowdninja.com]":                        "acme.example",
		"Re: [Relayed on behalf of a@acme.example to x@bugcrowdninja.com]":                       "", // not at the start
		"[Relayed on behalf of nobody to x@bugcrowdninja.com] Hi":                                "",
		"Your account has been updated.":                                                         "",
	} {
		if got := relayedApex(subject); got != want {
			t.Errorf("%q: %q, want %q", subject, got, want)
		}
	}
}

func TestOnPlatform(t *testing.T) {
	ps := []store.ProgramRef{{ID: 1, Platform: "bugcrowd"}, {ID: 2, Platform: "hackerone"}, {ID: 3, Platform: "bugcrowd"}}
	for plat, want := range map[string][]int64{"": {1, 2, 3}, "bugcrowd": {1, 3}, "intigriti": nil} {
		var ids []int64
		for _, p := range onPlatform(ps, plat) {
			ids = append(ids, p.ID)
		}
		if !slices.Equal(ids, want) {
			t.Errorf("%q: %v, want %v", plat, ids, want)
		}
	}
}
