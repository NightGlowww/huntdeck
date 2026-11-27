package web

import (
	"context"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The hunt board's Invites box: every invitation waiting to be accepted,
// then the private programs joined in the last boardInviteDays.
const (
	boardPending    = 5 // pending invitations listed
	boardJoined     = 3 // recently joined ones listed
	boardInviteDays = 30
)

// acceptURLs are where each platform's pending invitations are accepted;
// others link the program's own page.
var acceptURLs = map[string]string{
	"intigriti": "https://app.intigriti.com/researcher/programs/invites-and-applications",
	"vulbox":    "https://user.vulbox.com/InfoCenter/receive",
	"gobugfree": "https://app.gobugfree.com/engagements",
}

// boardInvites is the Invites box.
type boardInvites struct {
	Pending      []boardInvite
	PendingTotal int
	Joined       []boardInvite // joined (or first listed) in the last boardInviteDays
	JoinedTotal  int
}

// boardInvite is one invitation in the box.
type boardInvite struct {
	store.ProgramListItem
	AcceptURL string // pending ones: where to accept it on the platform
	// Accept is a pending one's Accept button, where HuntDeck can accept
	// it itself (else the box links AcceptURL).
	Accept *acceptForm
	// Since is when it was joined, else when HuntDeck listed it as new
	// (for ones accepted before HuntDeck saw them); Listed says which.
	Since  time.Time
	Listed bool
}

// Any reports whether the box has something to show.
func (b boardInvites) Any() bool { return len(b.Pending) > 0 || len(b.Joined) > 0 }

func (s *server) boardInvites(ctx context.Context, now time.Time) (boardInvites, error) {
	var b boardInvites
	pending, total, err := s.Store.Programs(ctx, store.ProgramFilter{Visibility: "private", Invite: "pending", State: "all",
		Sort: invitesList.DefaultSort, Limit: boardPending})
	if err != nil {
		return b, err
	}
	b.PendingTotal = total
	for _, p := range pending {
		u := acceptURLs[p.Platform]
		if u == "" {
			u = p.URL
		}
		b.Pending = append(b.Pending, boardInvite{ProgramListItem: p, AcceptURL: u, Accept: s.acceptFor(p, "/")})
	}
	joined, _, err := s.Store.Programs(ctx, store.ProgramFilter{Visibility: "private", Invite: "joined", State: "all",
		Sort: invitesList.DefaultSort, Limit: 100})
	if err != nil {
		return b, err
	}
	since := now.Add(-boardInviteDays * 24 * time.Hour)
	for _, p := range joined {
		// Joined, else listed as new: a program of the platform's first
		// import or of a newly signed-in account was the user's already.
		var in boardInvite
		switch {
		case p.JoinedAt != nil:
			in = boardInvite{ProgramListItem: p, Since: *p.JoinedAt}
		case p.ListedAt != nil:
			in = boardInvite{ProgramListItem: p, Since: *p.ListedAt, Listed: true}
		default:
			continue
		}
		if in.Since.Before(since) {
			continue
		}
		b.JoinedTotal++
		if len(b.Joined) < boardJoined {
			b.Joined = append(b.Joined, in)
		}
	}
	return b, nil
}
