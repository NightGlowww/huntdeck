package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

// The steps that change the account, each safe to run again.

// migrationsTable is wrangler's own, so `wrangler d1 migrations apply`
// still knows what's applied after HuntDeck set HuntMail up, and the other
// way round.
const migrationsTable = `CREATE TABLE IF NOT EXISTS d1_migrations(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT UNIQUE,
	applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

type queryResult struct {
	Results []map[string]any `json:"results"`
	Success bool             `json:"success"`
}

func (r *run) query(ctx context.Context, sql, what string) ([]queryResult, error) {
	var out []queryResult
	err := r.cf.call(ctx, http.MethodPost, "/accounts/"+r.account+"/d1/database/"+r.dbID+"/query",
		map[string]any{"sql": sql}, &out, what, "D1 Edit")
	return out, err
}

func (r *run) migrate(ctx context.Context) (string, error) {
	if _, err := r.query(ctx, migrationsTable, "prepare the database"); err != nil {
		return "", err
	}
	res, err := r.query(ctx, "SELECT name FROM d1_migrations", "read the database's migrations")
	if err != nil {
		return "", err
	}
	applied := map[string]bool{}
	for _, q := range res {
		for _, row := range q.Results {
			if name, ok := row["name"].(string); ok {
				applied[name] = true
			}
		}
	}
	n := 0
	for _, name := range r.b.Migrations {
		if applied[name] {
			continue
		}
		// The name is checked (migrationName): letters, digits, _ and .sql.
		sql := strings.TrimRight(r.b.migrations[name], " \t\r\n;") + ";\nINSERT INTO d1_migrations (name) VALUES ('" + name + "');"
		if _, err := r.query(ctx, sql, "apply migration "+name); err != nil {
			return "", err
		}
		n++
	}
	switch n {
	case 0:
		return "up to date", nil
	case 1:
		return "1 applied", nil
	default:
		return fmt.Sprintf("%d applied", n), nil
	}
}

// uploadWorker uploads the bundle's modules with its settings: the user's
// own settings (vars) kept over the bundle's defaults, the domains added
// to DOMAINS, and the secrets kept.
func (r *run) uploadWorker(ctx context.Context) (string, error) {
	vars := maps.Clone(r.b.Vars)
	if vars == nil {
		vars = map[string]string{}
	}
	maps.Copy(vars, r.vars)
	domains := splitList(vars["DOMAINS"])
	for _, z := range r.zones {
		if !slices.Contains(domains, z.Name) {
			domains = append(domains, z.Name)
		}
	}
	slices.Sort(domains)
	vars["DOMAINS"] = strings.Join(domains, ",")
	r.vars = vars

	// Maps, not binding: a plain_text binding needs its text even when
	// it's empty, which binding's omitempty would drop.
	bindings := []map[string]string{{"type": "d1", "name": r.b.D1Binding, "id": r.dbID}}
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		bindings = append(bindings, map[string]string{"type": "plain_text", "name": name, "text": vars[name]})
	}
	meta := map[string]any{
		"main_module":         r.b.MainModule,
		"compatibility_date":  r.b.CompatibilityDate,
		"compatibility_flags": nonNil(r.b.CompatibilityFlags),
		"bindings":            bindings,
		"keep_bindings":       []string{"secret_text"},
	}
	if len(r.b.Observability) > 0 {
		meta["observability"] = r.b.Observability
	}
	body, ctype, err := r.multipart(meta)
	if err != nil {
		return "", err
	}
	if err := r.cf.upload(ctx, r.script(), body, ctype, nil, "upload HuntMail", "Workers Scripts Edit"); err != nil {
		return "", err
	}
	if r.exists {
		return "updated", nil
	}
	return "uploaded", nil
}

func (r *run) multipart(meta map[string]any) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, "", err
	}
	part := func(name, ctype string, body []byte) error {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, name))
		h.Set("Content-Type", ctype)
		pw, err := w.CreatePart(h)
		if err == nil {
			_, err = pw.Write(body)
		}
		return err
	}
	if err := part("metadata", "application/json", raw); err != nil {
		return nil, "", err
	}
	for _, m := range r.b.Modules {
		if err := part(m.Name, moduleTypes[m.Type], r.b.files[m.Name]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// putSecrets makes the API key and private base path, when they're new.
func (r *run) putSecrets(ctx context.Context) (string, error) {
	if !r.newKey {
		return "kept", nil
	}
	key, base := randomHex(32), randomHex(16)
	for _, s := range [][2]string{{"API_KEY", key}, {"BASE_PATH", base}} {
		if err := r.cf.call(ctx, http.MethodPut, r.script()+"/secrets",
			map[string]string{"name": s[0], "text": s[1], "type": "secret_text"}, nil, "set the "+s[0]+" secret", "Workers Scripts Edit"); err != nil {
			return "", err
		}
	}
	r.key, r.base = key, base
	return "made", nil
}

func (r *run) schedule(ctx context.Context) (string, error) {
	crons := make([]map[string]string, 0, len(r.b.Crons))
	for _, c := range r.b.Crons {
		crons = append(crons, map[string]string{"cron": c})
	}
	if err := r.cf.call(ctx, http.MethodPut, r.script()+"/schedules", crons, nil, "schedule HuntMail's cron", "Workers Scripts Edit"); err != nil {
		return "", err
	}
	switch {
	case len(crons) == 0:
		return "none", nil
	case len(crons) == 1 && r.b.Crons[0] == "* * * * *":
		return "every minute", nil
	}
	return "cron " + strings.Join(r.b.Crons, ", "), nil
}

func (r *run) enableSubdomain(ctx context.Context) (string, error) {
	err := r.cf.call(ctx, http.MethodPost, r.script()+"/subdomain", map[string]bool{"enabled": true, "previews_enabled": false},
		nil, "turn on the workers.dev address", "Workers Scripts Edit")
	if err != nil {
		return "", err
	}
	return "on", nil
}

// route turns Email Routing on for a domain (adding its MX and SPF
// records) and points its catch-all at HuntMail.
func (r *run) route(ctx context.Context, z zone) (string, error) {
	var st struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	}
	base := "/zones/" + z.ID + "/email/routing"
	if err := r.cf.call(ctx, http.MethodGet, base, nil, &st, "read "+z.Name+"'s Email Routing", "Zone Settings Edit"); err != nil && !notFound(err) {
		return "", err
	}
	var did []string
	if !st.Enabled || st.Status != "ready" {
		if err := r.cf.call(ctx, http.MethodPost, base+"/dns", map[string]any{}, nil, "turn on Email Routing for "+z.Name, "Zone Settings Edit"); err != nil {
			return "", fmt.Errorf("%w. Mail records %s already has may be in the way: remove its MX records, or turn Email Routing on in Cloudflare's dashboard, then run this again", err, z.Name)
		}
		did = append(did, "Email Routing turned on")
	}
	var c catchAll
	if err := r.cf.call(ctx, http.MethodGet, base+"/rules/catch_all", nil, &c, "read "+z.Name+"'s catch-all", "Email Routing Rules Edit"); err != nil && !notFound(err) {
		return "", err
	}
	if !r.ours(c) {
		rule := map[string]any{
			"name":     "huntmail catch-all",
			"enabled":  true,
			"matchers": []map[string]string{{"type": "all"}},
			"actions":  []map[string]any{{"type": "worker", "value": []string{r.o.Name}}},
		}
		if err := r.cf.call(ctx, http.MethodPut, base+"/rules/catch_all", rule, nil, "point "+z.Name+"'s catch-all at HuntMail", "Email Routing Rules Edit"); err != nil {
			return "", err
		}
		did = append(did, "catch-all → HuntMail")
	}
	if len(did) == 0 {
		return "already set", nil
	}
	return strings.Join(did, ", "), nil
}

// waitHealthy asks HuntMail's health check until it answers, for about a
// minute: a new workers.dev address takes a moment to come up.
func (r *run) waitHealthy(ctx context.Context) bool {
	for i := range 12 {
		if i > 0 && r.o.Sleep(ctx, 5*time.Second) != nil {
			return false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cmp(r.o.HealthAt, r.origin())+"/"+r.base+"/health", nil)
		if err != nil {
			return false
		}
		if res, err := r.o.Health.Do(ctx, req); err == nil && res.StatusCode == http.StatusOK {
			return true
		}
	}
	return false
}
