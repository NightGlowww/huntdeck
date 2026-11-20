// Package scanhandoff sends the targets HuntDeck finds to a scanner: one MCP
// task per batch, each target recorded so a re-poll does not queue it twice.
// It lives on its own so the poll loop and the page run the same code.
package scanhandoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Run creates the tasks and returns how many. manual skips the enabled check:
// the button is the operator saying "now" whatever the schedule says.
// Options are what the caller can supply beyond the store and the scanner.
// Opinion is optional and advisory: nil means the deterministic checks are the
// whole decision.
type Options struct {
	Opinion SecondOpinion
	// Targets, when set, keeps only these: the operator naming what to scan,
	// rather than taking the next batch off the top of the list.
	Targets []string
}

// MinInterval is the gap a scheduled run leaves between handoffs. The scanner
// runs one task per node at a time, so a second batch inside this window only
// fills its queue; the operator's "send now" is not held back by it.
const MinInterval = 60 * time.Second

// ErrBusy is the scanner refusing work because its own queue is full or its
// creation limit is reached. It is capacity, not breakage: the operator's answer
// is to wait, and the page says so in words rather than in red.
var ErrBusy = errors.New("the scanner is queued: nothing was created, try again when it has caught up")

func Run(ctx context.Context, st *store.Store, client *scopesentry.Client, manual bool) (int, error) {
	return RunWith(ctx, st, client, manual, Options{})
}

// RunWith is Run with a second opinion: a decision model is asked about the
// batch before anything is created, and its block or confirm answer is
// respected. It can only narrow what the deterministic checks allow.
func RunWith(ctx context.Context, st *store.Store, client *scopesentry.Client, manual bool, opt Options) (int, error) {
	cfg, err := st.ScanConfigRow(ctx)
	if err != nil {
		return 0, err
	}
	// A scheduled run leaves a gap: the scanner runs one task per node at a
	// time, so handing over again before it has started the last batch only
	// fills its queue. The operator pressing "send now" is not held back.
	_, last, _ := st.ScanHandoffCount(ctx)
	if !manual && !last.IsZero() && time.Since(last) < MinInterval {
		return 0, nil
	}
	if !manual && !cfg.Enabled {
		return 0, nil
	}
	if cfg.TemplateID == "" || cfg.NodeName == "" {
		return 0, fmt.Errorf("pick a template and a node first")
	}
	// The scanner's host has to have room for what we are about to ask of it.
	if err := CheckHeadroom(ctx, client); err != nil {
		return 0, err
	}
	// Where it goes: the configured node when it has room, else the
	// least-queued online one. A full node is capacity, not failure.
	node, err := chooseNode(ctx, client, cfg.NodeName)
	if err != nil {
		return 0, fmt.Errorf("%w: every online node is at its queue limit", err)
	}
	if node != cfg.NodeName {
		slog.InfoContext(ctx, "scan: the configured node has no room", "configured", cfg.NodeName, "using", node)
	}
	perTask := cfg.PerTask
	if perTask <= 0 {
		perTask = 50
	}
	// Oldest first: the newest-first window meant a backlog never drained while
	// new scope kept arriving - the same program was scanned again and again and
	// everything older never was.
	cands, err := st.ScanBacklog(ctx, cfg.Kinds, perTask*4)
	if len(opt.Targets) > 0 {
		// A named handoff asks for exactly these, so it looks past the batch size.
		cands, err = st.ScanCandidates(ctx, cfg.Kinds, 5000)
		if err == nil {
			cands = onlyTargets(cands, opt.Targets)
		}
	}
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}
	// The second opinion, if one is configured, is asked once about the whole
	// batch: a per-task question would cost a call each and answer the same
	// thing.
	if opt.Opinion != nil {
		free, known, _ := headroom(client)
		if err := runOpinion(ctx, opt.Opinion, State{
			Tasks: (len(cands) + perTask - 1) / perTask, Targets: len(cands), PerTask: perTask,
			Template: cfg.TemplateID, Node: node, HostFreePct: free, HostKnown: known,
		}); err != nil {
			return 0, err
		}
	}

	// One program per task: a task mixing programs cannot be attributed to a
	// scope, and its name would say nothing about what is being scanned. The
	// candidates are grouped first, then chunked, so a program's targets stay
	// together and the name carries the program.
	created := 0
	for _, group := range groupByProgram(cands) {
		for start := 0; start < len(group.items); start += perTask {
			end := start + perTask
			if end > len(group.items) {
				end = len(group.items)
			}
			batch := group.items[start:end]
			targets := make([]string, 0, len(batch))
			for _, c := range batch {
				targets = append(targets, c.Target)
			}
			name := taskName(group.platform, group.label, created+1)
			id, err := client.CreateTask(ctx, name, cfg.TemplateID, node, targets)
			if err != nil {
				// The scanner refusing work for capacity is not a fault: stop here
				// and say so, rather than reporting a failure the operator cannot act on.
				if scopesentry.IsBusy(err) {
					return created, fmt.Errorf("%w: %w", ErrBusy, err)
				}
				return created, err
			}
			// Record before starting, not after. The task exists either way, and a
			// record that lags behind reality is how the same targets got handed over
			// twice: thirteen tasks sat parked in the scanner while their targets
			// stayed eligible for the next handoff. Starting is a separate act.
			if err := st.MarkHandedOff(ctx, batch, id); err != nil {
				return created, err
			}
			if err := client.StartTask(ctx, id); err != nil {
				return created, fmt.Errorf("task %s created and recorded but not started (start it in the scanner): %w", id, err)
			}
			created++
		}
	}
	return created, nil
}

// runOpinion asks the model and turns its answer into a verdict the handoff
// obeys. It is separate from RunWith so the rule - block and confirm stop
// the batch, an unanswered model is not permission - can be tested without
// a store, a scanner or a network.
func runOpinion(ctx context.Context, opinion SecondOpinion, st State) error {
	// A model that cannot be reached has not judged the batch. Refusing every
	// handoff because a third party is unreachable or geo-blocked made the
	// whole feature unusable, which is worse than losing the advisory: the
	// deterministic checks still govern, and they are the ones that matter.
	v, err := opinion.Judge(ctx, st)
	if err != nil {
		slog.WarnContext(ctx, "scan: the second opinion did not answer", "error", err)
		return nil
	}
	// One line either way: without it, a proceed leaves no trace and "did the
	// model run?" can only be answered by the absence of a warning.
	slog.InfoContext(ctx, "scan: second opinion", "choice", v.Choice, "confidence", v.Confidence, "model", v.Model)
	switch v.Choice {
	case ActBlock, ActConfirmFirst:
		return fmt.Errorf("the decision model says %s (confidence %.2f): no scan tasks created", v.Choice, v.Confidence)
	}
	return nil
}

// programGroup is one program's share of a handoff, in the order the candidates
// arrived.
type programGroup struct {
	platform, handle string
	label            string // what the task is called after: the name, not a UUID
	items            []store.ScanCandidate
}

// groupByProgram keeps each program's targets together, first appearance first.
func groupByProgram(cands []store.ScanCandidate) []programGroup {
	index := map[string]int{}
	seen := map[string]bool{}
	var groups []programGroup
	for _, c := range cands {
		// The same host can be in two programs' scope (the scan page lists it twice),
		// and a handoff that sends it twice scans it twice. The record is keyed by
		// target anyway, so the first program keeps it.
		if seen[c.Target] {
			continue
		}
		seen[c.Target] = true
		key := c.Platform + "/" + c.Handle
		if i, ok := index[key]; ok {
			groups[i].items = append(groups[i].items, c)
			continue
		}
		index[key] = len(groups)
		label := c.Name
		if strings.TrimSpace(label) == "" {
			label = c.Handle // a source with no name keeps the handle
		}
		groups = append(groups, programGroup{platform: c.Platform, handle: c.Handle, label: label, items: []store.ScanCandidate{c}})
	}
	return groups
}

// taskName says what is being scanned: the program and the moment. The counter
// separates the tasks one handoff creates for the same program.
func taskName(platformName, handle string, n int) string {
	name := "huntdeck-" + slug(platformName) + "-" + slug(handle) + "-" + time.Now().UTC().Format("20060102-150405")
	if n > 1 {
		name += "-" + strconv.Itoa(n)
	}
	return name
}

// slug keeps a name to what a task list can show on one line.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// onlyTargets keeps the candidates named, in the order they were named.
func onlyTargets(cands []store.ScanCandidate, wanted []string) []store.ScanCandidate {
	byTarget := make(map[string]store.ScanCandidate, len(cands)*2)
	for _, c := range cands {
		byTarget[c.Target] = c
		// The queue names a URL entry by its host, so a target typed from the queue
		// has to find the entry it belongs to.
		if c.Host != "" && c.Host != c.Target {
			if _, taken := byTarget[c.Host]; !taken {
				byTarget[c.Host] = c
			}
		}
	}
	out := make([]store.ScanCandidate, 0, len(wanted))
	for _, w := range wanted {
		if c, ok := byTarget[w]; ok {
			out = append(out, c)
		}
	}
	return out
}
