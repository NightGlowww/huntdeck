package weblogin

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The bug this pins: a login triggered by a poll was canceled halfway because
// the poll was, and the page it had filled in was never submitted. The error
// read "context canceled", which looks like the platform refusing - it was us
// hanging up.
func TestLoginSurvivesThePollBeingCanceled(t *testing.T) {
	h := &History{Now: time.Now}
	poll, cancel := context.WithCancel(context.Background())
	cancel() // the poll is already gone when the login starts

	var sawCanceled bool
	err := h.Login(poll, func(ctx context.Context) error {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			sawCanceled = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("login failed because the poll was canceled: %v", err)
	}
	if sawCanceled {
		t.Fatal("the login inherited the poll cancellation")
	}
}

// It still has a deadline of its own: a hung page must not block forever.
func TestLoginKeepsItsOwnDeadline(t *testing.T) {
	h := &History{Now: time.Now}
	if LoginBudget > 10*time.Minute {
		t.Fatalf("LoginBudget %v is long enough to hang a shutdown", LoginBudget)
	}
	var hasDeadline bool
	_ = h.Login(context.Background(), func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return errors.New("stop here")
	})
	if !hasDeadline {
		t.Fatal("the login runs without a deadline")
	}
}
