// Package version reports the build version of HuntDeck.
package version

import "runtime/debug"

// Version is set at build time with
// -ldflags "-X github.com/mdpsec/huntdeck/internal/version.Version=v1.2.3".
var Version = "dev"

// Pending is how many files had uncommitted changes when the binary was built;
// 0 means the build matches its commit exactly.
var Pending = "0"

// String returns the version plus the VCS revision when the binary was built
// from a git checkout.
func String() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return Version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
		if Pending != "" && Pending != "0" {
			rev += " " + Pending + " pending"
		}
	}
	return Version + " (" + rev + ")"
}
