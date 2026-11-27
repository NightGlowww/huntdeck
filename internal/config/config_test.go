package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func source(env map[string]string, files map[string]string) Source {
	return Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != DefaultListen || cfg.DataDir != DefaultDataDir || cfg.LogFormat != "text" || cfg.LogLevel != slog.LevelInfo ||
		cfg.Timezone != "America/New_York" || cfg.ChromeTimezone != "America/New_York" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.DBPath() != "/data/huntdeck.db" || cfg.BackupDir() != "/data/backups" {
		t.Errorf("paths: %s %s", cfg.DBPath(), cfg.BackupDir())
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(source(map[string]string{
		"HUNTDECK_LISTEN":           "127.0.0.1:9000",
		"HUNTDECK_DATA_DIR":         "/tmp/hd",
		"HUNTDECK_LOG_LEVEL":        "debug",
		"HUNTDECK_LOG_FORMAT":       "JSON",
		"HUNTDECK_TIMEZONE":         "Australia/Brisbane",
		"HUNTDECK_FEEDBACK_EMAIL":   "hello@example.com",
		"HUNTDECK_CLIENT_IP_HEADER": "CF-Connecting-IP",
		"HUNTDECK_PUBLIC_URL":       "https://demo.example.com/",
		"HUNTDECK_X_ACCOUNT":        "@huntdeck",
		"HUNTDECK_REPO_URL":         "https://github.com/example/huntdeck/",
		"HUNTDECK_LANGUAGE":         "ZH",
		"HUNTDECK_PASSWORD":         "a long enough password",
	}, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9000" || cfg.DataDir != "/tmp/hd" || cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "json" ||
		cfg.Timezone != "Australia/Brisbane" || cfg.ChromeTimezone != "Australia/Brisbane" || cfg.FeedbackEmail != "hello@example.com" ||
		cfg.ClientIPHeader != "CF-Connecting-IP" || cfg.PublicURL != "https://demo.example.com" || cfg.XAccount != "huntdeck" ||
		cfg.RepoURL != "https://github.com/example/huntdeck" || cfg.Language != "zh" || cfg.Password.Reveal() != "a long enough password" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadFromFile(t *testing.T) {
	cfg, err := Load(source(
		map[string]string{"HUNTDECK_DATA_DIR_FILE": "/run/secrets/dir"},
		map[string]string{"/run/secrets/dir": "/srv/huntdeck\n"},
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != "/srv/huntdeck" {
		t.Errorf("DataDir = %q, want trimmed file contents", cfg.DataDir)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		wants []string
	}{
		{"both value and file", map[string]string{"HUNTDECK_LISTEN": ":1", "HUNTDECK_LISTEN_FILE": "/x"}, []string{"both set"}},
		{"missing file", map[string]string{"HUNTDECK_LISTEN_FILE": "/nope"}, []string{`HUNTDECK_LISTEN_FILE: cannot read "/nope"`}},
		{"bad listen", map[string]string{"HUNTDECK_LISTEN": "8080"}, []string{"HUNTDECK_LISTEN"}},
		{"empty data dir", map[string]string{"HUNTDECK_DATA_DIR": ""}, []string{"HUNTDECK_DATA_DIR"}},
		{"bad format", map[string]string{"HUNTDECK_LOG_FORMAT": "xml"}, []string{"HUNTDECK_LOG_FORMAT"}},
		{"bad level", map[string]string{"HUNTDECK_LOG_LEVEL": "loud"}, []string{"HUNTDECK_LOG_LEVEL"}},
		{"h1 bad interval", map[string]string{"H1_FULL_INTERVAL": "soon"}, []string{"H1_FULL_INTERVAL"}},
		{"h1 interval too short", map[string]string{"H1_QUICK_INTERVAL": "10s"}, []string{"H1_QUICK_INTERVAL"}},
		{"h1 rate too high", map[string]string{"H1_RATE": "50"}, []string{"H1_RATE"}},
		{"h1 concurrency", map[string]string{"H1_CONCURRENCY": "0"}, []string{"H1_CONCURRENCY"}},
		{"bc public only not bool", map[string]string{"BC_PUBLIC_ONLY": "maybe"}, []string{"BC_PUBLIC_ONLY"}},
		{"client ip header not a header", map[string]string{"HUNTDECK_CLIENT_IP_HEADER": "CF-Connecting-IP: x"}, []string{"HUNTDECK_CLIENT_IP_HEADER"}},
		{"feedback not an address", map[string]string{"HUNTDECK_FEEDBACK_EMAIL": "not an email"}, []string{"HUNTDECK_FEEDBACK_EMAIL"}},
		{"feedback with a name", map[string]string{"HUNTDECK_FEEDBACK_EMAIL": "Us <hello@example.com>"}, []string{"HUNTDECK_FEEDBACK_EMAIL"}},
		{"public url not https", map[string]string{"HUNTDECK_PUBLIC_URL": "http://demo.example.com"}, []string{"HUNTDECK_PUBLIC_URL"}},
		{"public url with a path", map[string]string{"HUNTDECK_PUBLIC_URL": "https://example.com/demo"}, []string{"HUNTDECK_PUBLIC_URL"}},
		{"x account not a handle", map[string]string{"HUNTDECK_X_ACCOUNT": "https://x.com/huntdeck"}, []string{"HUNTDECK_X_ACCOUNT"}},
		{"seed url not https", map[string]string{"HUNTDECK_SEED_URL": "http://example.com/seed.db.gz"}, []string{"HUNTDECK_SEED_URL"}},
		{"seed url not a url", map[string]string{"HUNTDECK_SEED_URL": "seed.db.gz"}, []string{"HUNTDECK_SEED_URL"}},
		{"x account too long", map[string]string{"HUNTDECK_X_ACCOUNT": "a_very_long_handle_name"}, []string{"HUNTDECK_X_ACCOUNT"}},
		{"bc rate too high", map[string]string{"BC_RATE": "10"}, []string{"BC_RATE"}},
		{"it rate", map[string]string{"IT_RATE": "0"}, []string{"IT_RATE"}},
		{"ywh public only not bool", map[string]string{"YWH_PUBLIC_ONLY": "sure"}, []string{"YWH_PUBLIC_ONLY"}},
		{"vb rate too high", map[string]string{"VB_RATE": "10"}, []string{"VB_RATE"}},
		{"vb concurrency", map[string]string{"VB_CONCURRENCY": "9"}, []string{"VB_CONCURRENCY"}},
		{"vb auto join not bool", map[string]string{"VB_AUTO_JOIN": "maybe"}, []string{"VB_AUTO_JOIN"}},
		{"vb public only not bool", map[string]string{"VB_PUBLIC_ONLY": "nope"}, []string{"VB_PUBLIC_ONLY"}},
		{"ih rate too high", map[string]string{"IH_RATE": "10"}, []string{"IH_RATE"}},
		{"ih concurrency", map[string]string{"IH_CONCURRENCY": "9"}, []string{"IH_CONCURRENCY"}},
		{"ih public only not bool", map[string]string{"IH_PUBLIC_ONLY": "nope"}, []string{"IH_PUBLIC_ONLY"}},
		{"gbf rate too high", map[string]string{"GBF_RATE": "10"}, []string{"GBF_RATE"}},
		{"gbf concurrency", map[string]string{"GBF_CONCURRENCY": "9"}, []string{"GBF_CONCURRENCY"}},
		{"gbf public only not bool", map[string]string{"GBF_PUBLIC_ONLY": "nope"}, []string{"GBF_PUBLIC_ONLY"}},
		{"ins rate too high", map[string]string{"INS_RATE": "10"}, []string{"INS_RATE"}},
		{"ins concurrency", map[string]string{"INS_CONCURRENCY": "9"}, []string{"INS_CONCURRENCY"}},
		{"ins interval too short", map[string]string{"INS_QUICK_INTERVAL": "10s"}, []string{"INS_QUICK_INTERVAL"}},
		{"bad proxy", map[string]string{"HUNTDECK_PROXY": "127.0.0.1:8080"}, []string{"HUNTDECK_PROXY"}},
		{"bad time zone", map[string]string{"HUNTDECK_TIMEZONE": "Brisbane"}, []string{"HUNTDECK_TIMEZONE"}},
		{"local time zone", map[string]string{"HUNTDECK_TIMEZONE": "Local"}, []string{"HUNTDECK_TIMEZONE"}},
		{"bad browser time zone", map[string]string{"HUNTDECK_CHROME_TIMEZONE": "New York"}, []string{"HUNTDECK_CHROME_TIMEZONE"}},
		{"short password", map[string]string{"HUNTDECK_PASSWORD": "short"}, []string{"at least 8 characters"}},
		{"language too long", map[string]string{"HUNTDECK_LANGUAGE": "chinese-simplified"}, []string{"HUNTDECK_LANGUAGE"}},
		{"huntmail url without key", map[string]string{"HUNTMAIL_URL": "https://mail.example.workers.dev/0123456789abcdef"}, []string{"HUNTMAIL_URL and HUNTMAIL_API_KEY"}},
		{"huntmail url without base path", map[string]string{"HUNTMAIL_URL": "https://mail.example.workers.dev/", "HUNTMAIL_API_KEY": "k"}, []string{"HUNTMAIL_URL: needs its base path"}},
		{"huntmail url not https", map[string]string{"HUNTMAIL_URL": "http://mail.example.com/0123456789abcdef", "HUNTMAIL_API_KEY": "k"}, []string{"HUNTMAIL_URL: must start with https"}},
		{"huntmail domain", map[string]string{"HUNTMAIL_DOMAINS": "example.com, not a domain"}, []string{"HUNTMAIL_DOMAINS"}},
		{"reports all problems", map[string]string{"HUNTDECK_LOG_FORMAT": "xml", "HUNTDECK_LOG_LEVEL": "loud"}, []string{"HUNTDECK_LOG_FORMAT", "HUNTDECK_LOG_LEVEL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(source(tt.env, nil))
			if err == nil {
				t.Fatal("expected error")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestFileErrorDoesNotLeakContents(t *testing.T) {
	src := source(map[string]string{"HUNTDECK_LISTEN_FILE": "/f"}, nil)
	src.ReadFile = func(string) ([]byte, error) { return nil, errors.New("s3cr3t-value") }
	_, err := Load(src)
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("error should not include the underlying read error: %v", err)
	}
}

func TestHackerOneConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.HackerOne
	if h.Enabled() || h.FullInterval != 6*time.Hour || h.QuickInterval != 15*time.Minute || h.RatePerSecond != 5 || h.Concurrency != 4 {
		t.Errorf("defaults: %+v", h)
	}

	cfg, err = Load(source(map[string]string{
		"H1_USERNAME": "me", "H1_TOKEN_FILE": "/run/secrets/h1", "H1_FULL_INTERVAL": "0",
		"H1_QUICK_INTERVAL": "30m", "H1_RATE": "2.5", "H1_CONCURRENCY": "2", "HUNTDECK_PROXY": "http://127.0.0.1:8081",
		"HUNTDECK_ALLOWED_HOSTS": " HuntDeck.Tailnet.ts.net, ,lan-box ",
	}, map[string]string{"/run/secrets/h1": "tok-xyz\n"}))
	if err != nil {
		t.Fatal(err)
	}
	h = cfg.HackerOne
	if !h.Enabled() || h.Token.Reveal() != "tok-xyz" || h.FullInterval != 0 || h.QuickInterval != 30*time.Minute ||
		h.RatePerSecond != 2.5 || h.Concurrency != 2 || cfg.Proxy == nil || cfg.Proxy.Host != "127.0.0.1:8081" {
		t.Errorf("parsed: %+v proxy=%v", h, cfg.Proxy)
	}
	if strings.Join(cfg.AllowedHosts, ",") != "huntdeck.tailnet.ts.net,lan-box" {
		t.Errorf("AllowedHosts = %q", cfg.AllowedHosts)
	}
}

func TestBugcrowdConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Bugcrowd
	if b.Enabled() || b.FullInterval != 6*time.Hour || b.QuickInterval != 30*time.Minute || b.RatePerSecond != 1 || b.Concurrency != 2 {
		t.Errorf("defaults: %+v", b)
	}

	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"public only", map[string]string{"BC_PUBLIC_ONLY": "true"}},
		{"session", map[string]string{"BC_SESSION": "abc"}},
		{"login", map[string]string{"BC_EMAIL": "me@example.com", "BC_PASSWORD": "p#ss$word!", "BC_TOTP_SECRET": "JBSWY3DPEHPK3PXP"}},
	} {
		cfg, err := Load(source(tt.env, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !cfg.Bugcrowd.Enabled() {
			t.Errorf("%s: not enabled", tt.name)
		}
	}
	cfg, _ = Load(source(map[string]string{"BC_EMAIL": "me@example.com", "BC_PASSWORD": "p#ss$word!"}, nil))
	if cfg.Bugcrowd.Password.Reveal() != "p#ss$word!" {
		t.Errorf("password not kept verbatim")
	}
}

func TestIntigritiAndYesWeHackConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Intigriti.Enabled() || cfg.YesWeHack.Enabled() {
		t.Fatal("platforms must be off by default")
	}
	if cfg.YesWeHack.QuickInterval != 30*time.Minute || cfg.Intigriti.RatePerSecond != 2 {
		t.Errorf("defaults: %+v %+v", cfg.YesWeHack, cfg.Intigriti)
	}
	cfg, err = Load(source(map[string]string{
		"IT_TOKEN": "t", "YWH_EMAIL": "me@example.com", "YWH_PASSWORD": "p",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Intigriti.Enabled() || !cfg.YesWeHack.Enabled() {
		t.Errorf("enabled: %+v %+v", cfg.Intigriti, cfg.YesWeHack)
	}
	if y, _ := Load(source(map[string]string{"YWH_PUBLIC_ONLY": "1"}, nil)); !y.YesWeHack.Enabled() {
		t.Error("YWH_PUBLIC_ONLY should enable YesWeHack")
	}
}

func TestVulBoxConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.VulBox
	if v.Enabled() || !v.AutoJoin || v.PublicOnly || v.FullInterval != 6*time.Hour || v.QuickInterval != 30*time.Minute || v.Rate != 1 || v.Concurrency != 2 {
		t.Errorf("defaults: %+v", v)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"public only", map[string]string{"VB_PUBLIC_ONLY": "true"}},
		{"session", map[string]string{"VB_SESSION": "tok-abc"}},
		{"login", map[string]string{"VB_ACCOUNT": "hunter@example.com", "VB_PASSWORD": "p#ss$word!", "VB_TOTP_SECRET": "JBSWY3DPEHPK3PXP", "VB_AUTO_JOIN": "false"}},
	} {
		cfg, err := Load(source(tt.env, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !cfg.VulBox.Enabled() {
			t.Errorf("%s: not enabled", tt.name)
		}
	}
	cfg, err = Load(source(map[string]string{"VB_ACCOUNT": "hunter@example.com", "VB_PASSWORD": "p#ss$word!", "VB_AUTO_JOIN": "false", "VB_RATE": "2.5", "VB_CONCURRENCY": "3"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VulBox.Password.Reveal() != "p#ss$word!" || cfg.VulBox.AutoJoin || cfg.VulBox.Rate != 2.5 || cfg.VulBox.Concurrency != 3 {
		t.Errorf("parsed: %+v", cfg.VulBox)
	}
}

func TestIssueHuntAndGObugfreeConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	ih := cfg.IssueHunt
	if ih.Enabled() || ih.PublicOnly || ih.FullInterval != 6*time.Hour || ih.QuickInterval != 30*time.Minute || ih.Rate != 1.5 || ih.Concurrency != 2 {
		t.Errorf("IssueHunt defaults: %+v", ih)
	}
	gbf := cfg.GObugfree
	if gbf.Enabled() || gbf.PublicOnly || gbf.FullInterval != 6*time.Hour || gbf.QuickInterval != 30*time.Minute || gbf.Rate != 1.5 || gbf.Concurrency != 2 {
		t.Errorf("GObugfree defaults: %+v", gbf)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"ih public only", map[string]string{"IH_PUBLIC_ONLY": "true"}},
		{"ih api key", map[string]string{"IH_API_KEY": "ihk_secret_token"}},
	} {
		cfg, err := Load(source(tt.env, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !cfg.IssueHunt.Enabled() {
			t.Errorf("%s: not enabled", tt.name)
		}
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"gbf public only", map[string]string{"GBF_PUBLIC_ONLY": "true"}},
		{"gbf token", map[string]string{"GBF_TOKEN": "tok-abc"}},
		{"gbf login", map[string]string{"GBF_EMAIL": "hunter@example.com", "GBF_PASSWORD": "p#ss$word!", "GBF_TOTP_SECRET": "JBSWY3DPEHPK3PXP"}},
	} {
		cfg, err := Load(source(tt.env, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !cfg.GObugfree.Enabled() {
			t.Errorf("%s: not enabled", tt.name)
		}
	}
	cfg, err = Load(source(map[string]string{
		"IH_API_KEY": "ihk_tok", "IH_RATE": "2.5", "IH_CONCURRENCY": "3", "IH_FULL_INTERVAL": "12h", "IH_QUICK_INTERVAL": "1h",
		"GBF_EMAIL": "hunter@example.com", "GBF_PASSWORD": "p#ss$word!", "GBF_RATE": "2.0", "GBF_CONCURRENCY": "4",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IssueHunt.APIKey.Reveal() != "ihk_tok" || cfg.IssueHunt.Rate != 2.5 || cfg.IssueHunt.Concurrency != 3 ||
		cfg.IssueHunt.FullInterval != 12*time.Hour || cfg.IssueHunt.QuickInterval != time.Hour {
		t.Errorf("IssueHunt parsed: %+v", cfg.IssueHunt)
	}
	if cfg.GObugfree.Password.Reveal() != "p#ss$word!" || cfg.GObugfree.Rate != 2.0 || cfg.GObugfree.Concurrency != 4 {
		t.Errorf("GObugfree parsed: %+v", cfg.GObugfree)
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg.IssueHunt), "ihk_tok") || strings.Contains(fmt.Sprintf("%+v", cfg.GObugfree), "p#ss$word!") {
		t.Error("secret printed in formatted output")
	}
}

func TestInspectivConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	i := cfg.Inspectiv
	if i.Enabled() || i.FullInterval != 6*time.Hour || i.QuickInterval != 30*time.Minute || i.Rate != 1.5 || i.Concurrency != 2 {
		t.Errorf("defaults: %+v", i)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"token", map[string]string{"INS_TOKEN": "0123456789abcdef0123456789abcdef01234567"}},
		{"login", map[string]string{"INS_EMAIL": "hunter@example.com", "INS_PASSWORD": "p#ss$word!"}},
	} {
		cfg, err := Load(source(tt.env, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !cfg.Inspectiv.Enabled() {
			t.Errorf("%s: not enabled", tt.name)
		}
	}
	cfg, err = Load(source(map[string]string{"INS_EMAIL": "hunter@example.com", "INS_PASSWORD": "p#ss$word!", "INS_TOKEN": "Token abcd", "INS_RATE": "2", "INS_CONCURRENCY": "3", "INS_FULL_INTERVAL": "12h"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	i = cfg.Inspectiv
	if i.Password.Reveal() != "p#ss$word!" || i.Token.Reveal() != "Token abcd" || i.Rate != 2 || i.Concurrency != 3 || i.FullInterval != 12*time.Hour {
		t.Errorf("parsed: %+v", i)
	}
	if out := fmt.Sprintf("%+v", i); strings.Contains(out, "p#ss$word!") || strings.Contains(out, "abcd") {
		t.Errorf("secret printed: %s", out)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret("tok-xyz")
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "value", s)
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %s %#v", s, s, s), fmt.Sprintf("%+v", HackerOne{Token: s}), buf.String()} {
		if strings.Contains(out, "tok-xyz") {
			t.Errorf("secret leaked: %s", out)
		}
	}
}

// A read-only copy starts only without anything that would poll or sign
// in, and says which settings to unset.
func TestReadOnly(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want []string // in the error; none: it starts
	}{
		{map[string]string{"HUNTDECK_READ_ONLY": "1"}, nil},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "H1_USERNAME": "u", "H1_TOKEN": "t"}, []string{"H1_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "BC_PUBLIC_ONLY": "1", "HUNTDECK_VNC": "1"}, []string{"BC_*", "HUNTDECK_VNC"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "YWH_PUBLIC_ONLY": "1", "HUNTDECK_PROXY": "http://127.0.0.1:3128"}, []string{"YWH_*", "HUNTDECK_PROXY"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "VB_SESSION": "tok"}, []string{"VB_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "IH_PUBLIC_ONLY": "1"}, []string{"IH_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "IH_API_KEY": "ihk_tok"}, []string{"IH_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "GBF_PUBLIC_ONLY": "1"}, []string{"GBF_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "GBF_TOKEN": "tok"}, []string{"GBF_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "INS_TOKEN": "tok"}, []string{"INS_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "INS_EMAIL": "me@example.com", "INS_PASSWORD": "p"}, []string{"INS_*"}},
		{map[string]string{"HUNTDECK_READ_ONLY": "1", "HUNTMAIL_URL": "https://mail.example.workers.dev/0123456789abcdef", "HUNTMAIL_API_KEY": "k"}, []string{"HUNTMAIL_URL"}},
		{map[string]string{"H1_USERNAME": "u", "H1_TOKEN": "t"}, nil},
	}
	for _, tt := range tests {
		cfg, err := Load(source(tt.env, nil))
		if len(tt.want) == 0 {
			if err != nil {
				t.Errorf("%v: %v", tt.env, err)
			}
			continue
		}
		for _, w := range tt.want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("%v: error %v doesn't name %s", tt.env, err, w)
			}
		}
		if !cfg.ReadOnly {
			t.Errorf("%v: not read-only", tt.env)
		}
	}
}

// A new install downloads the public starter seed unless told otherwise.
func TestSeedURL(t *testing.T) {
	tests := []struct{ set, want string }{
		{"", DefaultSeedURL}, // unset
		{"off", ""},
		{"OFF", ""},
		{"none", ""},
		{"https://seeds.example.com/my.db.gz", "https://seeds.example.com/my.db.gz"},
	}
	for _, tt := range tests {
		env := map[string]string{}
		if tt.set != "" {
			env["HUNTDECK_SEED_URL"] = tt.set
		}
		cfg, err := Load(source(env, nil))
		if err != nil || cfg.SeedURL != tt.want {
			t.Errorf("HUNTDECK_SEED_URL=%q: %q, %v; want %q", tt.set, cfg.SeedURL, err, tt.want)
		}
	}
	if !strings.HasPrefix(DefaultSeedURL, "https://github.com/mdpsec/huntdeck/releases/download/seed/") {
		t.Errorf("DefaultSeedURL = %s", DefaultSeedURL)
	}
}

// HuntMail: off by default; its address and key go together, its domains
// are cleaned up, and the address (which holds a secret path) never prints
// in an error.
func TestHuntMailConfig(t *testing.T) {
	cfg, err := Load(source(nil, nil))
	if err != nil || cfg.HuntMail.Enabled() {
		t.Fatalf("off by default: %+v %v", cfg.HuntMail, err)
	}
	cfg, err = Load(source(map[string]string{"HUNTMAIL_URL": " https://mail.example.workers.dev/0123456789abcdef/ ", "HUNTMAIL_API_KEY": "k",
		"HUNTMAIL_DOMAINS": "Example.com, @example.net"}, nil))
	if err != nil || !cfg.HuntMail.Enabled() || strings.Join(cfg.HuntMail.Domains, ",") != "example.com,example.net" {
		t.Fatalf("set up: %+v %v", cfg.HuntMail, err)
	}
	_, err = Load(source(map[string]string{"HUNTMAIL_URL": "https://mail.example.workers.dev/s3cretpath?x=1", "HUNTMAIL_API_KEY": "k"}, nil))
	if err == nil || strings.Contains(err.Error(), "s3cretpath") {
		t.Errorf("bad address: %v", err)
	}
}
