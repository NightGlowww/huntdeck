package main

import (
	"context"
	"net"
	"strings"
	"testing"
)

// HUNTDECK_PROXY applies to every platform request, so a dead one stops all
// polling; doctor says so in one line instead of leaving a wall of "giving up
// after 5 attempts" in the poll history.
func TestDoctorProxyReportsADeadProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now

	t.Setenv("HUNTDECK_PROXY", "http://"+addr)
	var lines []string
	doctorProxy(context.Background(),
		func(f string, a ...any) { lines = append(lines, "ok") },
		func(f string, a ...any) { lines = append(lines, "FAIL") })
	if len(lines) != 1 || lines[0] != "FAIL" {
		t.Fatalf("a refused proxy must fail the check, got %v", lines)
	}
}

func TestDoctorProxyPassesWhenUnset(t *testing.T) {
	t.Setenv("HUNTDECK_PROXY", "")
	called := false
	doctorProxy(context.Background(),
		func(f string, a ...any) { called = true },
		func(f string, a ...any) { called = true })
	if called {
		t.Error("an unset HUNTDECK_PROXY must not report anything")
	}
}

func TestDoctorProxyAcceptsAReachableOne(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	t.Setenv("HUNTDECK_PROXY", "http://"+ln.Addr().String())
	var got string
	doctorProxy(context.Background(),
		func(f string, a ...any) { got = "ok" },
		func(f string, a ...any) { got = "FAIL" })
	if got != "ok" || !strings.Contains(ln.Addr().String(), ":") {
		t.Fatalf("a listening proxy must pass, got %q", got)
	}
}
