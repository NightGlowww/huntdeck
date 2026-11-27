package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// HUNTDECK_SOURCES names each custom source and where its document is.
func TestSources(t *testing.T) {
	for _, tt := range []struct {
		name, env string
		want      []CustomSource
		err       string
	}{
		{"none", "", nil, ""},
		{"a file and an address", "inspectiv=/data/sources/inspectiv.json  Acme-2=https://feeds.example.net/acme.json",
			[]CustomSource{{"inspectiv", "/data/sources/inspectiv.json"}, {"acme-2", "https://feeds.example.net/acme.json"}}, ""},
		{"no location", "inspectiv=", nil, "not name=location"},
		{"relative path", "inspectiv=sources/x.json", nil, "neither an absolute file path"},
		{"other scheme", "inspectiv=ftp://example.net/x.json", nil, "neither an absolute file path"},
		{"taken name", "hackerone=/data/x.json", nil, "taken by a platform"},
		{"taken vulbox name", "vulbox=/data/x.json", nil, "taken by a platform"},
		{"taken vb alias", "vb=/data/x.json", nil, "taken by a platform"},
		{"bad name", "in_spectiv=/data/x.json", nil, "lowercase letters"},
		{"twice", "a1=/data/a.json a1=/data/b.json", []CustomSource{{"a1", "/data/a.json"}}, "named twice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(source(map[string]string{"HUNTDECK_SOURCES": tt.env}, nil))
			if !slices.Equal(cfg.Sources.List, tt.want) {
				t.Errorf("sources = %+v, want %+v", cfg.Sources.List, tt.want)
			}
			if (tt.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tt.err) {
				t.Errorf("err = %v, want %q", err, tt.err)
			}
			if cfg.Sources.Interval != time.Hour {
				t.Errorf("interval = %v", cfg.Sources.Interval)
			}
		})
	}
	if _, err := Load(source(map[string]string{"HUNTDECK_READ_ONLY": "1", "HUNTDECK_SOURCES": "a1=/data/a.json"}, nil)); err == nil ||
		!strings.Contains(err.Error(), "unset HUNTDECK_SOURCES") {
		t.Errorf("read-only with sources: %v", err)
	}
}
