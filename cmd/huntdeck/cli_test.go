package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// A diagnostic that waits forever becomes part of an outage: during the lock-up
// two stuck "huntdeck status" runs held the locks the server needed. The bound is
// 90 seconds, and the message says what a timeout means rather than leaving a
// silent exit.
func TestReadOnlyCommandsGiveUpAndSayWhy(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := readOnlyContext(&buf)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("readOnlyContext has no deadline")
	}
	if left, _ := ctx.Deadline(); time.Until(left) > readOnlyTimeout {
		t.Fatalf("deadline is further out than %s", readOnlyTimeout)
	}
}

// And the message is printed once the deadline has passed.
func TestReadOnlyTimeoutMessage(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	explainTimeout(ctx, &buf)
	if !strings.Contains(buf.String(), "locked by another process") {
		t.Fatalf("no explanation printed: %q", buf.String())
	}
}
