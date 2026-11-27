package main

import (
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/platform/gobugfree"
	"github.com/mdpsec/huntdeck/internal/platform/inspectiv"
	"github.com/mdpsec/huntdeck/internal/platform/issuehunt"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// The scheduler and syncer find optional features by type assertion, so the
// jobs must carry the platform client itself, not the configured wrapper.
func TestScheduleJobsKeepOptionalInterfaces(t *testing.T) {
	jobs := scheduleJobs([]configured{{Platform: fake.New(), concurrency: 2}})
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want full and quick", len(jobs))
	}
	for _, j := range jobs {
		if _, ok := j.Platform.(platform.StatsFetcher); !ok {
			t.Errorf("%s job's platform (%T) hides platform.StatsFetcher", j.Mode, j.Platform)
		}
	}
}

func TestBuildPlatformsAndAliasesNewPlatforms(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(),
		IssueHunt: config.IssueHunt{
			PublicOnly:    true,
			FullInterval:  6 * time.Hour,
			QuickInterval: 30 * time.Minute,
			Rate:          1.5,
			Concurrency:   2,
		},
		GObugfree: config.GObugfree{
			Email:         "hunter@example.com",
			Password:      "secret",
			FullInterval:  6 * time.Hour,
			QuickInterval: 30 * time.Minute,
			Rate:          1.5,
			Concurrency:   2,
		},
		Inspectiv: config.Inspectiv{
			Email:         "hunter@example.com",
			Password:      "secret",
			FullInterval:  6 * time.Hour,
			QuickInterval: 30 * time.Minute,
			Rate:          1.5,
			Concurrency:   2,
		},
	}
	host := weblogin.NewHost(weblogin.Chrome{Profile: t.TempDir()})
	ps, err := buildPlatforms(cfg, host)
	if err != nil {
		t.Fatalf("buildPlatforms: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d platforms, want 3", len(ps))
	}
	for alias, want := range map[string]string{
		"ih":  issuehunt.Name,
		"gbf": gobugfree.Name,
		"ins": inspectiv.Name,
	} {
		picked, err := pickPlatforms(ps, alias)
		if err != nil {
			t.Errorf("pickPlatforms(%q): %v", alias, err)
			continue
		}
		if len(picked) != 1 || picked[0].Name() != want {
			t.Errorf("pickPlatforms(%q) = %v, want %s", alias, picked, want)
		}
	}
}
