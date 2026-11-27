package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// CustomSource is a platform HuntDeck has no client for, read from a JSON
// document in HuntDeck's own format (internal/platform/custom) that a
// script of the user's writes: HUNTDECK_SOURCES. It's polled like a
// platform, under its own name.
type CustomSource struct {
	Name     string // the platform name it's stored under, e.g. "inspectiv"
	Location string // an absolute file path, or an http(s) address
}

// Sources are the custom sources, and how often they're read.
type Sources struct {
	List []CustomSource
	// HUNTDECK_SOURCES_INTERVAL: how often each is read in full (its new
	// programs are picked up every quarter of that).
	Interval time.Duration
}

// sourceName is a custom source's name: lowercase letters, digits and
// dashes, starting with a letter.
var sourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// reservedSources are names a custom source can't take: the platforms
// HuntDeck has clients for, their short names, and programs added by hand.
var reservedSources = []string{"hackerone", "bugcrowd", "intigriti", "yeswehack", "vulbox", "issuehunt", "gobugfree", "inspectiv", "independent", "h1", "bc", "it", "ywh", "vb", "ih", "gbf", "ins", "fake"}

// parseSources reads HUNTDECK_SOURCES: name=location pairs separated by
// spaces, as "inspectiv=/data/sources/inspectiv.json
// acme=https://feeds.example.net/acme.json".
func parseSources(v string) ([]CustomSource, []error) {
	var out []CustomSource
	var errs []error
	for _, pair := range strings.Fields(v) {
		name, loc, ok := strings.Cut(pair, "=")
		name = strings.ToLower(name)
		switch {
		case !ok || loc == "":
			errs = append(errs, fmt.Errorf("HUNTDECK_SOURCES %q: not name=location", pair))
			continue
		case !sourceName.MatchString(name):
			errs = append(errs, fmt.Errorf("HUNTDECK_SOURCES %q: a name is 2 to 31 lowercase letters, digits and dashes", name))
			continue
		case slices.Contains(reservedSources, name):
			errs = append(errs, fmt.Errorf("HUNTDECK_SOURCES %q: that name is taken by a platform HuntDeck knows", name))
			continue
		case slices.ContainsFunc(out, func(s CustomSource) bool { return s.Name == name }):
			errs = append(errs, fmt.Errorf("HUNTDECK_SOURCES %q: named twice", name))
			continue
		}
		if !filepath.IsAbs(loc) {
			u, err := url.Parse(loc)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				errs = append(errs, fmt.Errorf("HUNTDECK_SOURCES %s: %q is neither an absolute file path nor an http(s) address", name, loc))
				continue
			}
		}
		out = append(out, CustomSource{Name: name, Location: loc})
	}
	return out, errs
}
