package weblogin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistory(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	path := filepath.Join(t.TempDir(), "sessions", "login.json")
	tries := 0
	failing := func(context.Context) error { tries++; return errors.New("x: login: the page didn't move on") }
	working := func(context.Context) error { tries++; return nil }

	tests := []struct {
		name      string
		after     time.Duration // since the first failure
		login     func(context.Context) error
		wantErr   string
		wantTries int
	}{
		{"a failure is recorded", 0, failing, "didn't move on", 1},
		{"and blocks the next login", 10 * time.Minute, working, "retrying after", 1},
		{"until RetryAfter has passed", RetryAfter + time.Minute, working, "", 2},
	}
	h := NewHistory(path)
	h.Now = clock
	start := now
	for _, tt := range tests {
		now = start.Add(tt.after)
		// A new History on the same file each time, as after a restart.
		h = NewHistory(path)
		h.Now = clock
		err := h.Login(context.Background(), tt.login)
		if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%s: err %v, want %q", tt.name, err, tt.wantErr)
		}
		if tries != tt.wantTries {
			t.Errorf("%s: %d tries, want %d", tt.name, tries, tt.wantTries)
		}
		if tt.wantErr == "retrying after" && !errors.Is(err, ErrWaiting) {
			t.Errorf("%s: %v doesn't wrap ErrWaiting", tt.name, err)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("history file %v, %v; want it private", fi, err)
	}
	ws := NewHistory(path).Report(true)
	if !ws.SignedIn || ws.Failure != "" || ws.LastBrowserLogin == nil || !ws.LastBrowserLogin.Equal(now) {
		t.Errorf("after a login that worked, from the file: %+v", ws)
	}
}

func TestRejectionSticks(t *testing.T) {
	h := NewHistory("")
	tries := 0
	refuse := func(context.Context) error { tries++; return &Rejection{"x: login", "Invalid password"} }
	for range 3 {
		if err := h.Login(context.Background(), refuse); !IsRejection(err) {
			t.Errorf("err %v, want the rejection", err)
		}
	}
	if tries != 1 || h.Stuck() == nil {
		t.Errorf("%d tries, stuck %v; want one try, then stuck", tries, h.Stuck())
	}
	if ws := h.Report(true); ws.SignedIn || !ws.Stuck || ws.RetryAfter != nil || !strings.Contains(ws.Failure, "Invalid password") {
		t.Errorf("report %+v, want refused for good", ws)
	}
}

func TestAccountCode(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 10, 0, time.UTC)
	a := &Account{Who: "x: login", TOTPEnv: "X_TOTP_SECRET", Now: func() time.Time { return now }}
	if _, err := a.Code(context.Background()); !IsRejection(err) || !strings.Contains(err.Error(), "set X_TOTP_SECRET") {
		t.Errorf("without a secret: %v, want a rejection naming the setting", err)
	}
	a.TOTPSecret = "not base32!"
	if _, err := a.Code(context.Background()); !IsRejection(err) {
		t.Errorf("a bad secret: %v, want a rejection", err)
	}
	a.TOTPSecret = "JBSWY3DPEHPK3PXP"
	first, err := a.Code(context.Background())
	if err != nil || len(first) != 6 {
		t.Fatalf("code %q, %v", first, err)
	}
	// The same step again waits for the next code; a cancelled wait stops.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Code(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a used step: %v, want to wait for the next (cancelled)", err)
	}
}

// The Settings text promised "1h, then 2h, 4h…" while the code waited thirty
// minutes every time, so a login that cannot work opened Chrome every half hour.
func TestLoginWaitDoublesAndIsCapped(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, RetryAfter}, {1, RetryAfter}, {2, 2 * RetryAfter}, {3, 4 * RetryAfter},
		{4, RetryAfterMax}, {9, RetryAfterMax},
	}
	for _, c := range cases {
		if got := waitAfter(c.failures); got != c.want {
			t.Errorf("waitAfter(%d) = %s, want %s", c.failures, got, c.want)
		}
	}
}
