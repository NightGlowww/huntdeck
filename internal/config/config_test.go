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
		{"h1 username without token", map[string]string{"H1_USERNAME": "me"}, []string{"must be set together"}},
		{"h1 bad interval", map[string]string{"H1_FULL_INTERVAL": "soon"}, []string{"H1_FULL_INTERVAL"}},
		{"h1 interval too short", map[string]string{"H1_QUICK_INTERVAL": "10s"}, []string{"H1_QUICK_INTERVAL"}},
		{"h1 rate too high", map[string]string{"H1_RATE": "50"}, []string{"H1_RATE"}},
		{"h1 concurrency", map[string]string{"H1_CONCURRENCY": "0"}, []string{"H1_CONCURRENCY"}},
		{"bc email without password", map[string]string{"BC_EMAIL": "me@example.com"}, []string{"BC_EMAIL and BC_PASSWORD"}},
		{"bc totp without login", map[string]string{"BC_TOTP_SECRET": "JBSWY3DPEHPK3PXP"}, []string{"BC_TOTP_SECRET needs"}},
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
		{"ywh email without password", map[string]string{"YWH_EMAIL": "me@example.com"}, []string{"YWH_EMAIL and YWH_PASSWORD"}},
		{"ywh totp without login", map[string]string{"YWH_TOTP_SECRET": "JBSWY3DPEHPK3PXP"}, []string{"YWH_TOTP_SECRET needs"}},
		{"it rate", map[string]string{"IT_RATE": "0"}, []string{"IT_RATE"}},
		{"ywh public only not bool", map[string]string{"YWH_PUBLIC_ONLY": "sure"}, []string{"YWH_PUBLIC_ONLY"}},
		{"bad proxy", map[string]string{"HUNTDECK_PROXY": "127.0.0.1:8080"}, []string{"HUNTDECK_PROXY"}},
		{"bad time zone", map[string]string{"HUNTDECK_TIMEZONE": "Brisbane"}, []string{"HUNTDECK_TIMEZONE"}},
		{"local time zone", map[string]string{"HUNTDECK_TIMEZONE": "Local"}, []string{"HUNTDECK_TIMEZONE"}},
		{"bad browser time zone", map[string]string{"HUNTDECK_CHROME_TIMEZONE": "New York"}, []string{"HUNTDECK_CHROME_TIMEZONE"}},
		{"short password", map[string]string{"HUNTDECK_PASSWORD": "short"}, []string{"at least 8 characters"}},
		{"language too long", map[string]string{"HUNTDECK_LANGUAGE": "chinese-simplified"}, []string{"HUNTDECK_LANGUAGE"}},
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
