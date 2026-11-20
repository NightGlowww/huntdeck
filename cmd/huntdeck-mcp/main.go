// Command huntdeck-mcp serves HuntDeck over the Model Context Protocol on
// stdio, so an agent can ask a running instance what it knows.
//
//	HUNTDECK_URL=http://127.0.0.1:8080 HUNTDECK_PASSWORD='…' huntdeck-mcp
//
// It talks to the instance's REST API and signs in with the access password
// the way a browser does; it adds no accounts of its own.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
	"github.com/mdpsec/huntdeck/internal/version"
)

func main() {
	base := os.Getenv("HUNTDECK_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	// HUNTDECK_MCP_READONLY=1 offers only the look-ups: no poll, nothing
	// that changes the instance.
	readOnly := os.Getenv("HUNTDECK_MCP_READONLY") == "1"
	s := mcpserver.New(base, os.Getenv("HUNTDECK_PASSWORD"), version.Version, readOnly)
	// A token from the API page is the credential for a script: with one set,
	// the client sends it as a bearer header and never makes a session.
	s.Token = os.Getenv("HUNTDECK_TOKEN")
	// The decision model, for the triage tool; empty means it says so.
	s.JevURL = os.Getenv("HUNTDECK_JEV_URL")
	s.JevKey = os.Getenv("HUNTDECK_JEV_KEY")
	s.JevModel = os.Getenv("HUNTDECK_JEV_MODEL")
	s.JevProxy = os.Getenv("HUNTDECK_JEV_PROXY")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := s.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "huntdeck-mcp:", err)
		os.Exit(1)
	}
}
