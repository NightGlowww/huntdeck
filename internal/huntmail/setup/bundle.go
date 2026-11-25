package setup

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
)

// HuntMail's code, as HuntMail's own `bundle` script writes it: the
// Worker's modules exactly as wrangler would upload them, its database
// migrations and a manifest saying how to deploy them. It's copied here
// from a HuntMail release (bundle/README.md), never edited by hand.
//
//go:embed all:bundle
var embedded embed.FS

// ErrNoBundle: this build of HuntDeck has no HuntMail to set up.
var ErrNoBundle = errors.New("this HuntDeck has no HuntMail bundle to set up")

// Manifest is the bundle's manifest.json.
type Manifest struct {
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	MainModule         string            `json:"main_module"`
	Modules            []Module          `json:"modules"`
	CompatibilityDate  string            `json:"compatibility_date"`
	CompatibilityFlags []string          `json:"compatibility_flags"`
	Crons              []string          `json:"crons"`
	Observability      json.RawMessage   `json:"observability"`
	D1Binding          string            `json:"d1_binding"`
	Vars               map[string]string `json:"vars"`
	Secrets            []string          `json:"secrets"`
	Migrations         []string          `json:"migrations"`
	MigrationsSHA256   map[string]string `json:"migrations_sha256"`
	MigrationsTable    string            `json:"migrations_table"`
	WorkersDev         *bool             `json:"workers_dev"`
}

// Module is one of the Worker's files.
type Module struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // esm, cjs, wasm, text or data
	SHA256 string `json:"sha256"`
}

// Bundle is a checked bundle, ready to deploy.
type Bundle struct {
	Manifest
	files      map[string][]byte // module name → its bytes
	migrations map[string]string // migration name → its SQL
}

// Version is the HuntMail version the bundle holds.
func (b *Bundle) Version() string { return b.Manifest.Version }

var (
	moduleName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	migrationName = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]{1,80}\.sql$`)
	bindingName   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	compatDate    = regexp.MustCompile(`^20[0-9]{2}-[01][0-9]-[0-3][0-9]$`)
	versionText   = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,40}$`)
)

// moduleTypes are the content types Cloudflare takes for each module type.
var moduleTypes = map[string]string{
	"esm":  "application/javascript+module",
	"cjs":  "application/javascript",
	"wasm": "application/wasm",
	"text": "text/plain",
	"data": "application/octet-stream",
}

// requiredSecrets are what HuntDeck generates, and needs, to read the inbox.
var requiredSecrets = []string{"API_KEY", "BASE_PATH"}

// Embedded returns the bundle built into HuntDeck, or ErrNoBundle.
func Embedded() (*Bundle, error) {
	sub, err := fs.Sub(embedded, "bundle")
	if err != nil {
		return nil, err
	}
	return Load(sub)
}

// Load reads and checks a bundle: every file the manifest names, with the
// hash it says, and nothing it can't deploy.
func Load(fsys fs.FS) (*Bundle, error) {
	raw, err := fs.ReadFile(fsys, "manifest.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoBundle
	}
	if err != nil {
		return nil, err
	}
	b := &Bundle{files: map[string][]byte{}, migrations: map[string]string{}}
	if err := json.Unmarshal(raw, &b.Manifest); err != nil {
		return nil, fmt.Errorf("huntmail bundle: manifest: %w", err)
	}
	m := &b.Manifest
	switch {
	case !versionText.MatchString(m.Version):
		return nil, errors.New("huntmail bundle: no version")
	case !compatDate.MatchString(m.CompatibilityDate):
		return nil, errors.New("huntmail bundle: no compatibility_date")
	case !bindingName.MatchString(m.D1Binding):
		return nil, errors.New("huntmail bundle: no d1_binding")
	case len(m.Modules) == 0 || !slices.ContainsFunc(m.Modules, func(x Module) bool { return x.Name == m.MainModule }):
		return nil, errors.New("huntmail bundle: main_module isn't one of its modules")
	}
	for _, s := range requiredSecrets {
		if !slices.Contains(m.Secrets, s) {
			return nil, fmt.Errorf("huntmail bundle: it doesn't use the %s secret", s)
		}
	}
	for name := range m.Vars {
		if !bindingName.MatchString(name) || slices.Contains(m.Secrets, name) || name == m.D1Binding {
			return nil, fmt.Errorf("huntmail bundle: bad var %q", name)
		}
	}
	for _, mod := range m.Modules {
		if !moduleName.MatchString(mod.Name) || moduleTypes[mod.Type] == "" {
			return nil, fmt.Errorf("huntmail bundle: bad module %q (%s)", mod.Name, mod.Type)
		}
		body, err := fs.ReadFile(fsys, mod.Name)
		if err != nil {
			return nil, fmt.Errorf("huntmail bundle: %w", err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != mod.SHA256 {
			return nil, fmt.Errorf("huntmail bundle: %s doesn't match its sha256", mod.Name)
		}
		b.files[mod.Name] = body
	}
	if m.MigrationsTable != "" && m.MigrationsTable != "d1_migrations" {
		return nil, fmt.Errorf("huntmail bundle: migrations table %q: this HuntDeck only knows wrangler's d1_migrations", m.MigrationsTable)
	}
	if m.WorkersDev != nil && !*m.WorkersDev {
		return nil, errors.New("huntmail bundle: it's off workers.dev, which is the only address HuntDeck can set up")
	}
	if !slices.IsSorted(m.Migrations) {
		return nil, errors.New("huntmail bundle: migrations out of order")
	}
	for _, name := range m.Migrations {
		if !migrationName.MatchString(name) {
			return nil, fmt.Errorf("huntmail bundle: bad migration name %q", name)
		}
		sql, err := fs.ReadFile(fsys, path.Join("migrations", name))
		if err != nil {
			return nil, fmt.Errorf("huntmail bundle: %w", err)
		}
		if sum := sha256.Sum256(sql); hex.EncodeToString(sum[:]) != m.MigrationsSHA256[name] {
			return nil, fmt.Errorf("huntmail bundle: migration %s doesn't match its sha256", name)
		}
		b.migrations[name] = string(sql)
	}
	return b, nil
}
