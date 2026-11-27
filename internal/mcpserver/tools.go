package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// tool is one MCP tool: its declaration and what it does.
type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	// Write is set on the tools that change something; a read-only
	// server leaves them out.
	Write bool
	// Local marks a tool that answers without asking the instance: the JSON
	// sweep parses what the fake answered, and there is no fake in its path.
	Local bool
	Call  func(ctx context.Context, s *Server, args map[string]any) (string, error)
}

// tools is the list an agent sees, in the order they're offered.
var tools = []tool{
	{
		Name:        "huntdeck_status",
		Description: "What the instance holds: programs and scope entries per platform, and when it last polled.",
		Schema:      object(nil),
		Call: func(ctx context.Context, s *Server, _ map[string]any) (string, error) {
			return s.get(ctx, "/api/v1/status", nil)
		},
	},
	{
		Name:        "huntdeck_programs",
		Description: "Search programs: HuntScore, bounty range, 90-day numbers. Filter by a word, platform, state or bounties.",
		Schema: object(map[string]any{
			"q":        prop("string", "Words to match in the name or handle"),
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"state":    prop("string", "open (default), paused, all or removed"),
			"bounty":   prop("string", "1 for programs that pay, vdp for those that don't"),
			"sort":     prop("string", "huntscore (default), name, bounty, paid90, received, resolved, response or launched"),
			"limit":    prop("integer", "How many to return (default 30, at most 1000, the API's own cap)"),
			"offset":   prop("integer", "Skip this many"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "q", "platform", "state", "bounty", "sort")
			q.Set("limit", strconv.Itoa(numberArg(args, "limit", 30)))
			if n := numberArg(args, "offset", 0); n > 0 {
				q.Set("offset", strconv.Itoa(n))
			}
			return s.get(ctx, "/api/v1/programs", q)
		},
	},
	{
		Name:        "huntdeck_programs_csv",
		Description: "The programs matching a search, as the page's spreadsheet: CSV text to save, every column, no paging.",
		Schema: object(map[string]any{
			"q":        prop("string", "Words to match in the name or handle"),
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"state":    prop("string", "open (default), paused, all or removed"),
			"bounty":   prop("string", "1 for programs that pay, vdp for those that don't"),
			"sort":     prop("string", "huntscore (default), name, bounty, paid90, received, resolved, response or launched"),
			"cols":     prop("string", "comma-separated column keys as the page's picker names them (hunt, bounty, ptotal, paid90, received, resolved, response, scope, hunters, new30, launched, changed, disclosed, req): platform, handle and name always lead; every column when omitted or unknown"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "q", "platform", "state", "bounty", "sort", "cols")
			q.Set("format", "csv")
			return s.get(ctx, "/api/v1/programs", q)
		},
	},
	{
		Name:        "huntdeck_program",
		Description: "One program in full: what it pays, its numbers, its policy, and its assets in scope.",
		Schema: object(map[string]any{
			"platform":     prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"handle":       prop("string", "the program's handle on its platform"),
			"parts":        prop("string", "what to include: scope, disclosed, facts, brief, announcements, rules (comma separated; default facts,brief,rules — a full program with 5,000 disclosures is a megabyte)"),
			"scope_limit":  prop("integer", "with scope: entries per answer (default 200, at most 5000)"),
			"scope_offset": prop("integer", "with scope: skip this many entries"),
		}, "platform", "handle"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			platform, handle := stringArg(args, "platform"), stringArg(args, "handle")
			if platform == "" || handle == "" {
				return "", fmt.Errorf("platform and handle are both needed")
			}
			q := url.Values{}
			parts := stringArg(args, "parts")
			if parts == "" {
				parts = "facts,brief,rules"
			}
			q.Set("parts", parts)
			if strings.Contains(parts, "scope") {
				q.Set("scope_limit", strconv.Itoa(numberArg(args, "scope_limit", 200)))
				if off := numberArg(args, "scope_offset", 0); off > 0 {
					q.Set("scope_offset", strconv.Itoa(off))
				}
			}
			return s.get(ctx, "/api/v1/programs/"+url.PathEscape(platform)+"/"+url.PathEscape(handle), q)
		},
	},
	{
		Name:        "huntdeck_invites",
		Description: "Private programs you were invited to: what the platform offered, when, and whether you joined. The page and the API have this list; the tools did not.",
		Schema: object(map[string]any{
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv or independent"),
			"sort":     prop("string", "newest (default), huntscore, bounty or name"),
			"limit":    prop("integer", "How many to return (default 30, at most 1000)"),
			"offset":   prop("integer", "Skip this many"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "platform", "sort")
			q.Set("limit", strconv.Itoa(numberArg(args, "limit", 30)))
			if n := numberArg(args, "offset", 0); n > 0 {
				q.Set("offset", strconv.Itoa(n))
			}
			return s.get(ctx, "/api/v1/invites", q)
		},
	},
	{
		Name:        "huntdeck_targets",
		Description: "In-scope targets (wildcards, domains, URLs, apps, APIs…) with the program that lists them and its bounty.",
		Schema: object(map[string]any{
			"kind":     prop("string", "wildcard, domain, url, api, ip, cidr, android, ios… (default domain)"),
			"platform": prop("string", "keep one platform's programs"),
			"q":        prop("string", "keep targets whose identifier contains this text"),
			"scope":    prop("string", "out keeps the out-of-scope list instead of the in-scope one"),
			"bounty":   prop("string", "1 to keep only bounty-eligible targets"),
			"added":    prop("string", "only targets added in the last 7d, 30d, 90d or 365d"),
			"tag":      prop("string", "keep targets of programs carrying this tag"),
			"sort":     prop("string", "best ranks by target score (how new and how worth it) instead of newest first"),
			"limit":    prop("integer", "How many to return (default 100, at most 5000, the API's own cap)"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "kind", "platform", "q", "scope", "bounty", "added", "tag", "sort")
			if q.Get("kind") == "" {
				// All kinds: a wildcard-only program has no domains, and a
				// default of "domain" would make its scope look empty.
				q.Set("kind", "all")
			}
			q.Set("limit", strconv.Itoa(numberArg(args, "limit", 100)))
			// The route answers plain text by default (its export); an agent
			// wants the structured list.
			q.Set("format", "json")
			return s.get(ctx, "/api/v1/targets", q)
		},
	},
	{
		Name: "huntdeck_match_many",
		Description: "Check a list of hosts, IPs or URLs (one per line, as a recon tool prints them) against every program's scope " +
			"in one call: which are in scope, under which program, and whether that program pays. The workflow it exists for: " +
			"subfinder output straight in, the in-scope subset out.",
		Schema: object(map[string]any{
			"hosts": prop("string", "one host, IP, URL or wildcard per line (at most 5000 here; the API takes 20000)"),
		}, "hosts"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			raw := stringArg(args, "hosts")
			lines := make([]string, 0, 64)
			for _, line := range strings.Split(raw, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					lines = append(lines, line)
				}
			}
			if len(lines) == 0 {
				return "", fmt.Errorf("hosts is empty: send one host, IP or URL per line")
			}
			const maxLines = 5000
			if len(lines) > maxLines {
				return "", fmt.Errorf("%d hosts is more than one call takes (%d): split the list", len(lines), maxLines)
			}
			return s.postText(ctx, "/api/v1/match", "text/plain; charset=utf-8", strings.Join(lines, "\n")+"\n")
		},
	},
	{
		Name:        "huntdeck_match",
		Description: "Which programs list a host, IP or URL in scope, and whether they pay a bounty.",
		Schema: object(map[string]any{
			"host": prop("string", "a hostname, IP, URL or wildcard to check"),
		}, "host"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			host := stringArg(args, "host")
			if host == "" {
				return "", fmt.Errorf("host is needed")
			}
			return s.get(ctx, "/api/v1/match", url.Values{"host": {host}})
		},
	},
	{
		Name:        "huntdeck_changes",
		Description: "What changed lately: new scope, removed scope, bounty changes, launches, pauses. Newest first.",
		Schema: object(map[string]any{
			"since":    prop("string", "how far back: 24h, 7d, 30d (default 24h)"),
			"platform": prop("string", "keep one platform"),
			"kind":     prop("string", "one kind or several separated by commas: scope_added, scope_removed, scope_updated, program_added, program_removed, state_changed, bounty_changed, rewards_changed, policy_changed, announcement, visibility_changed, program_renamed"),
			"program":  prop("string", "one program as platform/handle, like hackerone/shopify"),
			"limit":    prop("integer", "How many to return (default 50, at most 200)"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "since", "platform", "kind", "program")
			q.Set("limit", strconv.Itoa(numberArg(args, "limit", 50)))
			return s.get(ctx, "/api/v1/changes", q)
		},
	},
	{
		Name:        "huntdeck_changes_text",
		Description: "Changes as plain text, one a line: date, program, what happened, asset. The page's own filters apply.",
		Schema: object(map[string]any{
			"since":    prop("string", "how far back: 24h, 7d, 30d (the page's choices)"),
			"platform": prop("string", "keep one platform"),
			"group":    prop("string", "scope or programs, to keep one kind-group"),
			"kind":     prop("string", "one kind or several separated by commas"),
			"bounty":   prop("string", "1 for paying programs, vdp for those that don't"),
			"program":  prop("string", "one program as platform/handle"),
			"sort":     prop("string", "name or platform instead of by date"),
			"rev":      prop("string", "1 to reverse the order"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "since", "platform", "group", "kind", "bounty", "program", "sort", "rev")
			q.Set("format", "txt")
			return s.get(ctx, "/changes", q)
		},
	},
	{
		Name:        "huntdeck_disclosed",
		Description: "Disclosed reports across the programs: title, severity, weakness, bounty, votes, and a link.",
		Schema: object(map[string]any{
			"q":        prop("string", "words to match in the title, program name or handle"),
			"platform": prop("string", "keep one platform"),
			"severity": prop("string", "critical, high, medium, low or none"),
			"cve":      prop("string", "1 to keep only reports that name a CVE"),
			"sort":     prop("string", "new (default), bounty or votes"),
			"limit":    prop("integer", "How many to return (default 50, at most 200)"),
		}),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "q", "platform", "severity", "cve", "sort")
			q.Set("limit", strconv.Itoa(numberArg(args, "limit", 50)))
			return s.get(ctx, "/api/v1/disclosed", q)
		},
	},
	{
		Name:        "huntdeck_scope_text",
		Description: "A program's scope entries as plain text, one identifier per line: every row the filters match (not just the first screenful), with the Scope tab's filters.",
		Schema: object(map[string]any{
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"handle":   prop("string", "the program's handle on its platform"),
			"show":     prop("string", "in (default), out or removed"),
			"type":     prop("string", "an asset type to keep, like wildcard or domain"),
			"since":    prop("string", "only entries added in the last 7d, 30d or 90d"),
		}, "platform", "handle"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			platform, handle := stringArg(args, "platform"), stringArg(args, "handle")
			if platform == "" || handle == "" {
				return "", fmt.Errorf("platform and handle are both needed")
			}
			q := query(args, "show", "type", "since")
			q.Set("tab", "scope")
			q.Set("format", "txt")
			return s.get(ctx, "/programs/"+url.PathEscape(platform)+"/"+url.PathEscape(handle), q)
		},
	},
	{
		Name:        "huntdeck_export",
		Description: "A program's scope as a file for Burp Suite, ZAP or Caido: the body to save, ready to import.",
		Schema: object(map[string]any{
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"handle":   prop("string", "the program's handle on its platform"),
			"format":   prop("string", "burp (JSON), zap (XML context) or caido (JSON); burp by default"),
		}, "platform", "handle"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			platform, handle := stringArg(args, "platform"), stringArg(args, "handle")
			if platform == "" || handle == "" {
				return "", fmt.Errorf("platform and handle are both needed")
			}
			format := strings.ToLower(stringArg(args, "format"))
			if format == "" {
				format = "burp"
			}
			file, ok := map[string]string{
				"burp":  "scope.burp.json",
				"zap":   "scope.zap.context",
				"caido": "scope.caido.json",
			}[format]
			if !ok {
				return "", fmt.Errorf("format %q is not one of burp, zap, caido", format)
			}
			return s.get(ctx, "/api/v1/programs/"+url.PathEscape(platform)+"/"+url.PathEscape(handle)+"/"+file, nil)
		},
	},
	{
		Name: "huntdeck_queue",
		Description: "HuntQueue: targets ranked by whether you can start today and what the work is worth per hour, " +
			"with the reasons behind each score, plus `gated`: the targets a hard gate keeps out and why " +
			"(an invitation to accept, requirements, paused). Built every half hour; the fastest way to decide what to do next.",
		Schema: object(map[string]any{
			"limit":    prop("integer", "How many rows (default 20, at most 100)"),
			"platform": prop("string", "Only this platform (hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv)"),
			"kind":     prop("string", "Only this kind of asset (wildcard, domain, url, ip, cidr, android, ios)"),
			"added":    prop("string", "Only targets added within this many days, written 7d"),
			"paying":   prop("boolean", "Only programs that pay bounties"),
			"one":      prop("boolean", "One row per program, its best target only"),
			"explain":  prop("boolean", "With true, each row carries its factor breakdown: what each input did to its score"),
		}, ""),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			// The same filters the page's chips set: a tool that ignores them
			// answers a different question than the one asked.
			q := query(args, "limit", "platform", "kind", "added")
			for _, key := range []string{"paying", "one"} {
				if b, ok := args[key].(bool); ok && b {
					q.Set(key, "1")
				}
			}
			if b, ok := args["explain"].(bool); ok && b {
				q.Set("explain", "1")
			}
			return s.get(ctx, "/api/v1/queue", q)
		},
	},
	{
		Name: "huntdeck_board",
		Description: "The Hunt board as one answer: totals, this week's scope churn and new programs, the programs that " +
			"reopened or paused, and the top hunts ranked by HuntScore. What to open a session with.",
		Schema: object(nil),
		Call: func(ctx context.Context, s *Server, _ map[string]any) (string, error) {
			return s.get(ctx, "/api/v1/board", nil)
		},
	},
	{
		Name: "huntdeck_stats",
		Description: "Platform totals: programs, bounties paid, reports, and how the numbers moved. " +
			"Filter and sort them the way /stats does, so the answer can be “who pays best” instead of everything.",
		Schema: object(map[string]any{
			"q":      prop("string", "keep platforms whose name contains this text"),
			"bounty": prop("string", "1 for platforms that pay, vdp for those that don't"),
			"sort":   prop("string", "paid, reports, resolved, response, bounty, programs or name"),
			"rev":    prop("string", "1 to reverse the sort"),
			"limit":  prop("integer", "How many platforms to return (default 50, at most 100)"),
			"offset": prop("integer", "Skip this many"),
		}, ""),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			q := query(args, "q", "bounty", "sort", "rev")
			if n := numberArg(args, "limit", 0); n > 0 {
				q.Set("limit", strconv.Itoa(n))
			}
			if n := numberArg(args, "offset", 0); n > 0 {
				q.Set("offset", strconv.Itoa(n))
			}
			return s.get(ctx, "/api/v1/stats", q)
		},
	},
	{
		Name:        "huntdeck_poll",
		Write:       true,
		Description: "Ask the instance to poll a platform now: a quick poll lists programs, a full one fetches every scope. At most once a minute — polls cost the platform.",
		Schema: object(map[string]any{
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
		}, "platform"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			platform := stringArg(args, "platform")
			if platform == "" {
				return "", fmt.Errorf("platform is needed")
			}
			if wait, ok := s.pollCooldown(platform); ok {
				return "", fmt.Errorf("a %s poll was asked for %s ago: wait %s before asking again", platform, wait.Round(time.Second), (pollCooldown - wait).Round(time.Second))
			}
			body, err := s.post(ctx, "/api/v1/poll/"+url.PathEscape(platform))
			if err != nil {
				return "", err
			}
			s.polled(platform)
			return body, nil
		},
	},
	{
		Name:        "huntdeck_triage",
		Description: "Ask the decision model whether a finding would survive triage, before it is submitted. Two probabilities come back: would a triager accept it, and do the supplied artefacts prove the impact. Needs HUNTDECK_JEV_KEY.",
		Local:       true,
		Schema: object(map[string]any{
			"title":    prop("string", "the finding in one line"),
			"target":   prop("string", "the host or endpoint it is on"),
			"in_scope": prop("string", "the scope situation, in a sentence"),
			"impact":   prop("string", "what an attacker gains"),
			"evidence": prop("string", "what artefacts exist"),
			"steps":    prop("string", "how it was done, when that matters"),
		}, "title", "in_scope", "impact", "evidence"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			return s.triage(ctx, args)
		},
	},
	{
		Name:        "huntdeck_policy_check",
		Description: "Ask the decision model whether a testing plan complies with a program's own policy text. The policy comes from the instance; the plan comes from you. Answers forbidden, check_first or allowed with P(comply). Needs HUNTDECK_JEV_KEY.",
		Local:       true,
		Schema: object(map[string]any{
			"platform": prop("string", "hackerone, bugcrowd, intigriti, yeswehack, vulbox, issuehunt, gobugfree, inspectiv, independent or custom"),
			"handle":   prop("string", "the program's handle"),
			"plan":     prop("string", "what you intend to do, in a sentence"),
		}, "platform", "handle", "plan"),
		Call: func(ctx context.Context, s *Server, args map[string]any) (string, error) {
			return s.policyCheck(ctx, args)
		},
	},
}

// toolList is the tools, declared for the client.
func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if s.ReadOnly && t.Write {
			continue
		}
		out = append(out, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
	}
	return out
}

// callTool runs one tool by name.
func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if wait, ready := s.allowCall(); !ready {
		return "", fmt.Errorf("too many calls: %d a minute is the budget, and the instance is shared; try again in %s", callBudget, wait.Round(time.Second))
	}
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		if s.ReadOnly && t.Write {
			return "", fmt.Errorf("%s changes the instance, and this server is read-only (HUNTDECK_MCP_READONLY)", name)
		}
		return t.Call(ctx, s, args)
	}
	return "", fmt.Errorf("no tool named %q", name)
}

// object is a JSON Schema object with the given properties.
func object(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

// query copies the string arguments that are set into a query.
func query(args map[string]any, keys ...string) url.Values {
	q := url.Values{}
	for _, k := range keys {
		if v := strings.TrimSpace(stringArg(args, k)); v != "" {
			q.Set(k, v)
		}
	}
	return q
}

func stringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func numberArg(args map[string]any, key string, fallback int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return fallback
}

// pollCooldown is how often the poll tool may ask for a poll, however many
// times an agent calls it: the platforms are shared, and a loop here would
// be a load test.
const pollCooldown = time.Minute

// pollCooldown reports how long ago this platform was asked for a poll, when
// that was too recent. It is per platform: polling Bugcrowd is no reason to
// refuse a HackerOne poll a moment later.
func (s *Server) pollCooldown(platform string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.lastPoll[platform]
	if !ok || at.IsZero() {
		return 0, false
	}
	since := s.now().Sub(at)
	if since >= pollCooldown {
		return 0, false
	}
	return since, true
}

// polled records that a poll of platform was asked for.
func (s *Server) polled(platform string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastPoll == nil {
		s.lastPoll = map[string]time.Time{}
	}
	s.lastPoll[platform] = s.now()
}

// callBudget is how many tool calls one MCP session may make in
// callWindow: an agent that loops on a query would otherwise have the
// instance's database answer it a thousand times a minute.
const (
	callBudget = 120
	callWindow = time.Minute
)

// allowCall reports whether another call fits in the window, and how long
// the caller should wait when it doesn't.
func (s *Server) allowCall() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-callWindow)
	kept := s.calls[:0]
	for _, t := range s.calls {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.calls = kept
	if len(s.calls) >= callBudget {
		return s.calls[0].Add(callWindow).Sub(s.now()), false
	}
	s.calls = append(s.calls, s.now())
	return 0, true
}
