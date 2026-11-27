package version

import "testing"

func TestFormatVersion(t *testing.T) {
	cases := []struct {
		name        string
		ver         string
		pending     string
		rev         string
		dirty       bool
		wantString  string
		wantDisplay string
	}{
		{
			name:        "clean release",
			ver:         "v1.4.0",
			pending:     "0",
			rev:         "a1b2c3d4e5f6",
			dirty:       false,
			wantString:  "v1.4.0 (a1b2c3d)",
			wantDisplay: "v1.4.0",
		},
		{
			name:        "clean prerelease with build metadata",
			ver:         "v1.4.1-dev.2+a1b2c3d",
			pending:     "0",
			rev:         "a1b2c3d4e5f6",
			dirty:       false,
			wantString:  "v1.4.1-dev.2+a1b2c3d",
			wantDisplay: "v1.4.1-dev.2+a1b2c3d",
		},
		{
			name:        "dirty prerelease with build metadata",
			ver:         "v1.4.1-dev.2+a1b2c3d",
			pending:     "3",
			rev:         "a1b2c3d4e5f6",
			dirty:       true,
			wantString:  "v1.4.1-dev.2+a1b2c3d (a1b2c3d-dirty 3 pending)",
			wantDisplay: "v1.4.1-dev.2+a1b2c3d-dirty",
		},
		{
			name:        "clean dev build",
			ver:         "dev",
			pending:     "0",
			rev:         "a1b2c3d4e5f6",
			dirty:       false,
			wantString:  "dev (a1b2c3d)",
			wantDisplay: "dev",
		},
		{
			name:        "dirty release build",
			ver:         "v1.4.0",
			pending:     "2",
			rev:         "a1b2c3d4e5f6",
			dirty:       true,
			wantString:  "v1.4.0 (a1b2c3d-dirty 2 pending)",
			wantDisplay: "v1.4.0-dirty",
		},
		{
			name:        "no vcs revision",
			ver:         "v1.4.0",
			pending:     "0",
			rev:         "",
			dirty:       false,
			wantString:  "v1.4.0",
			wantDisplay: "v1.4.0",
		},
		{
			name:        "empty version fallback",
			ver:         "",
			pending:     "0",
			rev:         "",
			dirty:       true,
			wantString:  "dev-dirty",
			wantDisplay: "dev-dirty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatString(tc.ver, tc.pending, tc.rev, tc.dirty); got != tc.wantString {
				t.Errorf("formatString() = %q, want %q", got, tc.wantString)
			}
			if got := formatDisplay(tc.ver, tc.dirty); got != tc.wantDisplay {
				t.Errorf("formatDisplay() = %q, want %q", got, tc.wantDisplay)
			}
		})
	}
}
