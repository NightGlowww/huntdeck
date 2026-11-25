package web

import (
	"context"
	"net/mail"
	"regexp"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntmail"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Which program mail sent to an address HuntDeck didn't make is for: a
// guess, shown as one, from the first of
//
//  1. the address starting with a program's name or handle ("acme-…",
//     "acme.signup@…"): the user named it so;
//  2. the sender's root domain in a program's scope;
//  3. the sender's root domain named as a program is (acme.example for
//     "Acme", whose scope may list only acme-app.example);
//  4. for mail a platform relays from its own domain (HackerOne's
//     @wearehackerone.com forwarder, say), the sender's name: the start of
//     one of that platform's programs' names ("Acme" for "Acme Corp"); or,
//     when the relay names the real sender in the subject as Bugcrowd's
//     does ("[Relayed on behalf of …@acme.example to …]"), that sender's
//     domain, by 2 and 3 among the platform's programs.
//
// Names are compared as MailSlug writes them (letters and digits); words
// any address or sender might use (test, news, shop…) never count as one.

// GuessMore is how many programs the guess names beyond its first.
func (m mailRow) GuessMore() int { return max(len(m.Guess)-1, 0) }

// notPrograms are senders' root domains that never say which program mail
// is for: the platforms' own notifications, and free mail.
var notPrograms = map[string]bool{
	"hackerone.com": true, "wearehackerone.com": true, "hackerone.net": true, "bugcrowd.com": true, "bugcrowdninja.com": true,
	"intigriti.com": true, "intigriti.io": true, "intigriti.me": true, "yeswehack.com": true, "yeswehack.ninja": true,
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true, "yahoo.com": true,
	"icloud.com": true, "me.com": true, "proton.me": true, "protonmail.com": true, "gmx.com": true, "aol.com": true,
}

// relayPlatforms are the platforms' own domains that pass a program's
// mail on under the program's name, by the platform each is.
var relayPlatforms = map[string]string{
	"wearehackerone.com": "hackerone", "bugcrowdninja.com": "bugcrowd", "intigriti.me": "intigriti", "yeswehack.ninja": "yeswehack",
}

// relayedSubject is how Bugcrowd's relay names the real sender, at the
// start of the subject.
var relayedSubject = regexp.MustCompile(`^\[Relayed on behalf of \S*@([A-Za-z0-9.-]+) to [^\]]*\]`)

// relayedApex is the root domain of the real sender a relay names in the
// subject, "" when it names none.
func relayedApex(subject string) string {
	m := relayedSubject.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil {
		return ""
	}
	return scope.RootDomain(strings.TrimSuffix(strings.ToLower(m[1]), "."))
}

// notNames are words too common in addresses and senders' domains to name
// a program by, even when one is called that.
var notNames = map[string]bool{
	"test": true, "tests": true, "testing": true, "mail": true, "email": true, "user": true, "users": true, "admin": true,
	"info": true, "news": true, "shop": true, "team": true, "hello": true, "hunt": true, "hunter": true, "signup": true,
	"account": true, "accounts": true, "login": true, "support": true, "noreply": true, "contact": true, "demo": true,
	"prod": true, "staging": true, "security": true, "example": true, "victim": true, "attacker": true, "bounce": true,
}

// minNameLen is the shortest name a guess goes by.
const minNameLen = 4

// nameWord splits an address's local part into words.
var nameWord = regexp.MustCompile(`[a-z0-9]+`)

// addressNames are the names an address could start with: its first word,
// and its first two together ("acme-corp-1" for "Acme Corp").
func addressNames(addr string) []string {
	local, _, _ := strings.Cut(strings.ToLower(addr), "@")
	words := nameWord.FindAllString(local, 2)
	var out []string
	if len(words) > 0 {
		out = append(out, words[0])
	}
	if len(words) > 1 && strings.Trim(words[1], "0123456789") != "" {
		out = append(out, words[0]+words[1])
	}
	return out
}

// isName says a word can name a program.
func isName(w string) bool { return len(w) >= minNameLen && !notNames[w] }

// senderApex is the root domain of the address the mail says it's from
// (the From header, else the envelope sender), "" when there's none.
func senderApex(e huntmail.Email) string {
	addr := e.From
	if a, err := mail.ParseAddress(e.HeaderFrom); err == nil {
		addr = a.Address
	}
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(addr)), "@")
	if !ok || domain == "" {
		return ""
	}
	return scope.RootDomain(strings.TrimSuffix(domain, "."))
}

// guessPrograms fills in, for rows whose address wasn't made for a
// program, the programs they're likeliest for, and why.
func (s *server) guessPrograms(ctx context.Context, rows []mailRow) {
	var todo []int
	for i := range rows {
		if rows[i].Program == nil {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	named, err := s.Store.ProgramKeys(ctx)
	if err != nil {
		s.Log.Warn("email: programs by name", "err", err)
		return
	}
	// 1. The address names it.
	apexOf := map[int]string{}
	relayOf := map[int]string{} // the platform that relayed it, for 2 and 3
	var apexes []string
	for _, i := range todo {
		for _, w := range addressNames(rows[i].To) {
			if ps := named[w]; isName(w) && len(ps) > 0 {
				rows[i].Guess, rows[i].GuessWhy = ps, "the address starts with "+w+", as this program is named"
			}
		}
		if rows[i].Guess != nil {
			continue
		}
		a := senderApex(rows[i].Email)
		if plat := relayPlatforms[a]; plat != "" {
			// 4. A platform relayed it: the sender's name says whose.
			if ps, name := relayedFor(rows[i].Email, plat, named); len(ps) > 0 {
				rows[i].Guess, rows[i].GuessWhy = ps, platformLabel(plat)+" passed it on from "+name+", as this program is named"
				continue
			}
			if a = relayedApex(rows[i].Subject); a == "" {
				continue
			}
			relayOf[i] = plat
		}
		if a != "" && !notPrograms[a] {
			apexOf[i] = a
			if !slices.Contains(apexes, a) {
				apexes = append(apexes, a)
			}
		}
	}
	// 2. The sender's domain is in scope.
	inScope, err := s.Store.ProgramsUnder(ctx, apexes)
	if err != nil {
		s.Log.Warn("email: programs by sender", "err", err)
		return
	}
	for i, a := range apexOf {
		sender, plat := "the sender's domain, "+a+",", relayOf[i]
		if plat != "" {
			sender = platformLabel(plat) + " passed it on from " + a + ", which"
		}
		if ps := onPlatform(inScope[a], plat); len(ps) > 0 {
			rows[i].Guess, rows[i].GuessWhy = ps, sender+" is in this program's scope"
			continue
		}
		// 3. The sender's domain is named as the program is.
		label, _, _ := strings.Cut(a, ".")
		if w := store.MailSlug(label); isName(w) {
			if ps := onPlatform(named[w], plat); len(ps) > 0 {
				rows[i].Guess, rows[i].GuessWhy = ps, sender+" is named as this program is"
			}
		}
	}
}

// onPlatform are the programs on platform, or all of them for "".
func onPlatform(ps []store.ProgramRef, platform string) []store.ProgramRef {
	if platform == "" {
		return ps
	}
	var out []store.ProgramRef
	for _, p := range ps {
		if p.Platform == platform {
			out = append(out, p)
		}
	}
	return out
}

// nameWords are a name's words, lowercased, an apostrophe dropped rather
// than splitting one ("Acme's Store" is acmes, store).
func nameWords(name string) []string {
	name = strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(name))
	return nameWord.FindAllString(name, -1)
}

// relayedFor are the platform's programs whose name or handle starts with
// the words of the name a relayed message's sender goes by, and that name.
func relayedFor(e huntmail.Email, platform string, named map[string][]store.ProgramRef) ([]store.ProgramRef, string) {
	from, err := mail.ParseAddress(e.HeaderFrom)
	if err != nil {
		return nil, ""
	}
	words := nameWords(from.Name)
	if len(words) == 0 || !isName(strings.Join(words, "")) || notNames[words[0]] || store.MailSlug(from.Name) == platform {
		return nil, ""
	}
	var out []store.ProgramRef
	seen := map[int64]bool{}
	for _, ps := range named {
		for _, p := range ps {
			if p.Platform != platform || seen[p.ID] {
				continue
			}
			for _, n := range []string{p.Name, p.Handle} {
				if w := nameWords(n); len(w) >= len(words) && slices.Equal(w[:len(words)], words) {
					seen[p.ID] = true
					out = append(out, p)
					break
				}
			}
		}
	}
	// Private first, then by name, as ProgramKeys lists them.
	slices.SortFunc(out, func(a, b store.ProgramRef) int {
		if a.Private != b.Private {
			if a.Private {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, from.Name
}
