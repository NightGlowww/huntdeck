package scanhandoff

import (
	"context"
	"errors"
	"testing"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

type fakeNodes struct {
	nodes []scopesentry.Node
	err   error
}

func (f fakeNodes) Nodes(context.Context) ([]scopesentry.Node, error) {
	return f.nodes, f.err
}

// The case that bit: node-sh-01 sat at its five-task limit while two other
// nodes were empty, and every handoff was refused. The configured node keeps
// the work whenever it has room, so the setting is not a lie.
func TestChooseNodeRespectsTheConfiguredNodeWhenItHasRoom(t *testing.T) {
	f := fakeNodes{nodes: []scopesentry.Node{
		{Name: "node-sh-01", State: "1", QueueLen: 4},
		{Name: "node-tx-sh-02", State: "1", QueueLen: 0},
	}}
	got, err := chooseNode(context.Background(), f, "node-sh-01")
	if err != nil || got != "node-sh-01" {
		t.Fatalf("chose %q (%v), want node-sh-01", got, err)
	}
}

// Full means full: the least-queued online node takes over, which is what makes
// a three-node setup actually usable.
func TestChooseNodeMovesOnWhenTheConfiguredOneIsFull(t *testing.T) {
	f := fakeNodes{nodes: []scopesentry.Node{
		{Name: "node-sh-01", State: "1", QueueLen: 5},
		{Name: "node-dev-02", State: "1", QueueLen: 3},
		{Name: "node-tx-sh-02", State: "1", QueueLen: 0},
	}}
	got, err := chooseNode(context.Background(), f, "node-sh-01")
	if err != nil || got != "node-tx-sh-02" {
		t.Fatalf("chose %q (%v), want node-tx-sh-02", got, err)
	}
}

// An offline configured node is no better than a full one.
func TestChooseNodeSkipsAnOfflineConfiguredNode(t *testing.T) {
	f := fakeNodes{nodes: []scopesentry.Node{
		{Name: "node-sh-01", State: "3", QueueLen: 0},
		{Name: "node-dev-02", State: "1", QueueLen: 2},
	}}
	got, err := chooseNode(context.Background(), f, "node-sh-01")
	if err != nil || got != "node-dev-02" {
		t.Fatalf("chose %q (%v), want node-dev-02", got, err)
	}
}

// Every node full is the scanner's capacity, not a fault: the caller reports
// ErrBusy and the page says "wait".
func TestChooseNodeReportsBusyWhenEveryNodeIsFull(t *testing.T) {
	f := fakeNodes{nodes: []scopesentry.Node{
		{Name: "a", State: "1", QueueLen: 5},
		{Name: "b", State: "1", QueueLen: 7},
		{Name: "c", State: "3", QueueLen: 0},
	}}
	if _, err := chooseNode(context.Background(), f, "a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

// Nodes that cannot be read are not a reason to refuse: the configured node is
// used and the scanner's own answer decides.
func TestChooseNodeFallsBackWhenNodesCannotBeRead(t *testing.T) {
	f := fakeNodes{err: errors.New("boom")}
	got, err := chooseNode(context.Background(), f, "node-sh-01")
	if err != nil || got != "node-sh-01" {
		t.Fatalf("chose %q (%v), want the configured node", got, err)
	}
}
