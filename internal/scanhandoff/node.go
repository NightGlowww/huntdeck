package scanhandoff

import (
	"context"

	"github.com/mdpsec/huntdeck/internal/scopesentry"
)

// MaxQueuePerNode is the scanner's own limit: it refuses a sixth queued task
// per node. Reading it here means HuntDeck stops before being refused.
const MaxQueuePerNode = 5

// nodeLister is the one thing this file needs from the scanner, so the choice
// can be tested without a server.
type nodeLister interface {
	Nodes(ctx context.Context) ([]scopesentry.Node, error)
}

// chooseNode picks where a batch goes.
//
// The operator's node takes it whenever it has room: they chose it on purpose,
// and silently moving work elsewhere would make the setting a lie. Only when it
// is offline or full does the least-queued online node take over - which is the
// case that actually bit: node-sh-01 sat at its five-task limit while two other
// nodes were empty, and every handoff was refused.
//
// Unreadable nodes are not a reason to refuse: the configured node is used and
// the scanner's own answer decides.
func chooseNode(ctx context.Context, l nodeLister, want string) (string, error) {
	nodes, err := l.Nodes(ctx)
	if err != nil || len(nodes) == 0 {
		return want, nil
	}
	for _, n := range nodes {
		if n.Name == want && n.Online() && n.QueueLen < MaxQueuePerNode {
			return want, nil
		}
	}
	best := scopesentry.Node{QueueLen: MaxQueuePerNode}
	for _, n := range nodes {
		if !n.Online() || n.QueueLen >= MaxQueuePerNode {
			continue
		}
		if n.QueueLen < best.QueueLen {
			best = n
		}
	}
	if best.Name == "" {
		return "", ErrBusy
	}
	return best.Name, nil
}
