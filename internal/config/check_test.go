package config

import (
	"strings"
	"testing"
)

// The outage this pins: a TOTP secret in the environment, the email and password
// it belongs to in Settings. The environment-only load saw no email and refused
// the whole configuration, so the container would not start.
//
// Load accepts each source on its own; Check answers for the joined picture.
func TestCheckAnswersForTheJoinedPicture(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "secret in the environment, login filled in later from Settings",
			cfg: Config{Intigriti: Intigriti{
				TOTPSecret: Secret("akncplgl3exyi2ouyacizgxquy3fculo"),
			}},
			wantErr: "IT_TOTP_SECRET needs IT_EMAIL and IT_PASSWORD",
		},
		{
			name: "the same, once Settings supplies the login",
			cfg: Config{Intigriti: Intigriti{
				Email:      "someone@example.com",
				Password:   Secret("a password"),
				TOTPSecret: Secret("akncplgl3exyi2ouyacizgxquy3fculo"),
				Token:      Secret("a token"),
			}},
		},
		{
			name:    "half a login pair",
			cfg:     Config{Intigriti: Intigriti{Email: "someone@example.com"}},
			wantErr: "IT_EMAIL and IT_PASSWORD must be set together",
		},
		{
			name:    "a web login with no API token",
			cfg:     Config{Intigriti: Intigriti{Email: "a@example.com", Password: Secret("p")}},
			wantErr: "IT_EMAIL needs IT_TOKEN",
		},
		{
			name:    "the same rule for Bugcrowd",
			cfg:     Config{Bugcrowd: Bugcrowd{TOTPSecret: Secret("x")}},
			wantErr: "BC_TOTP_SECRET needs BC_EMAIL and BC_PASSWORD",
		},
		{
			name:    "and for YesWeHack",
			cfg:     Config{YesWeHack: YesWeHack{TOTPSecret: Secret("x")}},
			wantErr: "YWH_TOTP_SECRET needs YWH_EMAIL and YWH_PASSWORD",
		},
		{
			name:    "VulBox half login pair",
			cfg:     Config{VulBox: VulBox{Account: "hunter@example.com"}},
			wantErr: "VB_ACCOUNT and VB_PASSWORD must be set together",
		},
		{
			name:    "VulBox TOTP without login",
			cfg:     Config{VulBox: VulBox{TOTPSecret: Secret("x")}},
			wantErr: "VB_TOTP_SECRET needs VB_ACCOUNT and VB_PASSWORD",
		},
		{
			name: "VulBox complete login",
			cfg:  Config{VulBox: VulBox{Account: "hunter@example.com", Password: Secret("p"), TOTPSecret: Secret("x")}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Check(c.cfg)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Check refused a complete configuration: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("Check = %v, want %q", err, c.wantErr)
			}
		})
	}
}

// Loading the environment alone must not run those rules: this is exactly the
// configuration that stopped the container.
func TestLoadDoesNotRunTheJoinedRules(t *testing.T) {
	src := source(map[string]string{
		"IT_TOTP_SECRET": "akncplgl3exyi2ouyacizgxquy3fculo",
	}, nil)
	cfg, err := Load(src)
	if err != nil {
		t.Fatalf("Load refused a secret whose login lives elsewhere: %v", err)
	}
	if cfg.Intigriti.TOTPSecret == "" {
		t.Fatal("the secret was not read")
	}
}
