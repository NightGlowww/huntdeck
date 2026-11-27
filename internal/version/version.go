// Package version reports the build version of HuntDeck.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at build time with
// -ldflags "-X github.com/mdpsec/huntdeck/internal/version.Version=v1.4.0".
var Version = "dev"

// Pending is how many files had uncommitted changes when the binary was built;
// 0 means the build matches its commit exactly.
var Pending = "0"

// String returns the full build version (including short VCS revision when
// available) for logs, CLI output, and deploy verification.
func String() string {
	rev, dirty := vcsRevision()
	return formatString(Version, Pending, rev, dirty)
}

// Display returns a concise SemVer string suitable for the web UI footer.
func Display() string {
	_, dirty := vcsRevision()
	return formatDisplay(Version, dirty)
}

func vcsRevision() (rev string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}

func formatString(ver, pending, rev string, dirty bool) string {
	if ver == "" {
		ver = "dev"
	}
	if rev == "" {
		if dirty && !strings.HasSuffix(ver, "-dirty") {
			return ver + "-dirty"
		}
		return ver
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if !dirty && strings.Contains(ver, "+") {
		return ver
	}
	if dirty {
		rev += "-dirty"
		if pending != "" && pending != "0" {
			rev += " " + pending + " pending"
		}
	}
	return ver + " (" + rev + ")"
}

func formatDisplay(ver string, dirty bool) string {
	if ver == "" {
		ver = "dev"
	}
	if dirty && !strings.HasSuffix(ver, "-dirty") {
		return ver + "-dirty"
	}
	return ver
}
