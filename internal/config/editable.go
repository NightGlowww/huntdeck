package config

// Kind is how an editable setting is entered.
type Kind string

// Kinds of editable settings.
const (
	KindText   Kind = "text"
	KindSecret Kind = "secret" // never shown again once saved
	KindBool   Kind = "bool"
	KindNumber Kind = "number"
)

// Field is a variable that can also be set in Settings, where a saved
// value takes precedence over the environment. The ones HuntDeck needs
// before it can start or be reached (listen address, data directory,
// allowed hosts, logging, VNC) stay in the environment only.
type Field struct {
	Name  string // the environment variable
	Group string // the platform's name, "huntmail" or "network"
	Label string
	Kind  Kind
	Help  string
	// Default is the value used when it's unset, shown as a hint.
	Default string
	// Aliases are other variables read for it when it isn't set
	// (H1_OTP for H1_TOTP_SECRET).
	Aliases []string
}

// Secret reports whether the field's value is never shown.
func (f Field) Secret() bool { return f.Kind == KindSecret }

// Groups of editable fields, in the order Settings shows them.
const (
	GroupHackerOne = "hackerone"
	GroupBugcrowd  = "bugcrowd"
	GroupIntigriti = "intigriti"
	GroupYesWeHack = "yeswehack"
	GroupVulBox    = "vulbox"
	GroupHuntMail  = "huntmail"
	GroupNetwork   = "network"
	// GroupAI is the optional model: an OpenAI-compatible endpoint that reads
	// a program's policy and states the rules it sets.
	GroupAI = "ai"
	// GroupPojia is the disclosed-report library: POJIA answers what a program
	// has historically paid for.
	GroupPojia = "pojia"
	// GroupScan is the scanner the handoff sends targets to.
	GroupScan = "scan"
	// GroupJev is the decision model: a typed, calibrated judgement asked
	// before a consequential action, never in place of the deterministic checks.
	GroupJev = "jev"
)

// Editable lists every field Settings can set.
var Editable = []Field{
	{Name: "H1_USERNAME", Group: GroupHackerOne, Label: "API username", Kind: KindText,
		Help: "From hackerone.com/settings/api_token/edit, with the token."},
	{Name: "H1_TOKEN", Group: GroupHackerOne, Label: "API token", Kind: KindSecret},
	{Name: "H1_EMAIL", Group: GroupHackerOne, Label: "Website email", Kind: KindText,
		Help: "Optional website login, for private programs' stats and invitations. It signs in in the login browser."},
	{Name: "H1_PASSWORD", Group: GroupHackerOne, Label: "Website password", Kind: KindSecret},
	{Name: "H1_TOTP_SECRET", Group: GroupHackerOne, Label: "2FA secret", Kind: KindSecret, Aliases: []string{"H1_OTP"},
		Help: "The authenticator app's secret (base32), if 2FA is on."},
	{Name: "H1_RATE", Group: GroupHackerOne, Label: "Requests per second", Kind: KindNumber, Default: "5", Help: "0.1 to 10."},
	{Name: "H1_CONCURRENCY", Group: GroupHackerOne, Label: "Parallel scope fetches", Kind: KindNumber, Default: "4", Help: "1 to 16."},

	{Name: "BC_EMAIL", Group: GroupBugcrowd, Label: "Email", Kind: KindText,
		Help: "Signs in in the login browser; HuntDeck signs in again by itself whenever the session ends."},
	{Name: "BC_PASSWORD", Group: GroupBugcrowd, Label: "Password", Kind: KindSecret},
	{Name: "BC_TOTP_SECRET", Group: GroupBugcrowd, Label: "2FA secret", Kind: KindSecret,
		Help: "The authenticator app's secret (base32), shown when you add the app in Bugcrowd's 2FA settings."},
	{Name: "BC_SESSION", Group: GroupBugcrowd, Label: "Session cookie", Kind: KindSecret,
		Help: "Instead of a login: _bugcrowd_session from your browser. Lasts about 14 days."},
	{Name: "BC_PUBLIC_ONLY", Group: GroupBugcrowd, Label: "Public programs without an account", Kind: KindBool},
	{Name: "BC_RATE", Group: GroupBugcrowd, Label: "Requests per second", Kind: KindNumber, Default: "1",
		Help: "0.1 to 3. Bugcrowd's firewall blocks fast clients for hours."},
	{Name: "BC_CONCURRENCY", Group: GroupBugcrowd, Label: "Parallel scope fetches", Kind: KindNumber, Default: "2", Help: "1 to 4."},

	{Name: "IT_TOKEN", Group: GroupIntigriti, Label: "API token", Kind: KindSecret,
		Help: "A personal access token, from your Intigriti profile > Personal access tokens."},
	{Name: "IT_EMAIL", Group: GroupIntigriti, Label: "Website email", Kind: KindText,
		Help: "Optional web login, for the program stats the API lacks. Its first login runs in the login browser, through the proxy."},
	{Name: "IT_PASSWORD", Group: GroupIntigriti, Label: "Website password", Kind: KindSecret},
	{Name: "IT_TOTP_SECRET", Group: GroupIntigriti, Label: "2FA secret", Kind: KindSecret,
		Help: "The authenticator app's secret (base32), if 2FA is on."},
	{Name: "IT_RATE", Group: GroupIntigriti, Label: "Requests per second", Kind: KindNumber, Default: "2", Help: "0.1 to 10."},
	{Name: "IT_CONCURRENCY", Group: GroupIntigriti, Label: "Parallel scope fetches", Kind: KindNumber, Default: "2", Help: "1 to 8."},

	{Name: "YWH_EMAIL", Group: GroupYesWeHack, Label: "Email", Kind: KindText,
		Help: "For private invites. Signs in in the login browser; the token then renews itself."},
	{Name: "YWH_PASSWORD", Group: GroupYesWeHack, Label: "Password", Kind: KindSecret},
	{Name: "YWH_TOTP_SECRET", Group: GroupYesWeHack, Label: "2FA secret", Kind: KindSecret,
		Help: "The authenticator app's secret (base32), if 2FA is on."},
	{Name: "YWH_TOKEN", Group: GroupYesWeHack, Label: "Bearer token", Kind: KindSecret,
		Help: "Instead of a login: a token from your browser. It expires within hours."},
	{Name: "YWH_PUBLIC_ONLY", Group: GroupYesWeHack, Label: "Public programs without an account", Kind: KindBool},
	{Name: "YWH_RATE", Group: GroupYesWeHack, Label: "Requests per second", Kind: KindNumber, Default: "2", Help: "0.1 to 5."},
	{Name: "YWH_CONCURRENCY", Group: GroupYesWeHack, Label: "Parallel scope fetches", Kind: KindNumber, Default: "2", Help: "1 to 4."},

	{Name: "VB_ACCOUNT", Group: GroupVulBox, Label: "Account", Kind: KindText,
		Help: "Username, phone number or email. Signs in in the login browser (open Settings > Browser with VNC if a CAPTCHA appears)."},
	{Name: "VB_PASSWORD", Group: GroupVulBox, Label: "Password", Kind: KindSecret},
	{Name: "VB_TOTP_SECRET", Group: GroupVulBox, Label: "2FA secret", Kind: KindSecret,
		Help: "The authenticator app's secret (base32), if 2FA is on."},
	{Name: "VB_SESSION", Group: GroupVulBox, Label: "Session token", Kind: KindSecret,
		Help: "Instead of a login: vulbox_token or a Cookie header from your browser."},
	{Name: "VB_AUTO_JOIN", Group: GroupVulBox, Label: "Auto-join eligible projects", Kind: KindBool, Default: "true",
		Help: "Confirm and join projects or agreements that unlock scope, skipping any that require a deposit or ticket."},
	{Name: "VB_PUBLIC_ONLY", Group: GroupVulBox, Label: "Public programs without an account", Kind: KindBool},
	{Name: "VB_RATE", Group: GroupVulBox, Label: "Requests per second", Kind: KindNumber, Default: "1",
		Help: "0.1 to 5. Keep it polite for VulBox's firewall."},
	{Name: "VB_CONCURRENCY", Group: GroupVulBox, Label: "Parallel scope fetches", Kind: KindNumber, Default: "2", Help: "1 to 4."},

	{Name: "HUNTMAIL_URL", Group: GroupHuntMail, Label: "Address", Kind: KindSecret,
		Help: "Your HuntMail's address with its base path, as its setup printed it: https://<worker>.<subdomain>.workers.dev/<base path>. Kept secret like the key."},
	{Name: "HUNTMAIL_API_KEY", Group: GroupHuntMail, Label: "API key", Kind: KindSecret},
	{Name: "HUNTMAIL_DOMAINS", Group: GroupHuntMail, Label: "Domains for new addresses", Kind: KindText,
		Help: "Comma-separated, such as example.com, example.net. Empty: the domains HuntMail was set up for."},

	{Name: "HUNTDECK_PROXY", Group: GroupNetwork, Label: "Proxy", Kind: KindSecret,
		Help: "For all platform traffic and the login browser, such as http://user:password@host:port (http:// for the login browser) or socks5://host:port."},
	{Name: "HUNTDECK_CHROME_TIMEZONE", Group: GroupNetwork, Label: "Login browser's time zone", Kind: KindText,
		Help: "Should match the proxy's country, such as America/New_York."},
	{Name: "HUNTDECK_AI_BASE_URL", Group: GroupAI, Label: "Endpoint", Kind: KindText,
		Help: "An OpenAI-compatible base URL, e.g. https://api.openai.com/v1 or a local server."},
	{Name: "HUNTDECK_AI_MODEL", Group: GroupAI, Label: "Model", Kind: KindText,
		Help: "The model name the endpoint knows, e.g. gpt-4o-mini."},
	{Name: "HUNTDECK_AI_KEY", Group: GroupAI, Label: "API key", Kind: KindSecret,
		Help: "Sent as a bearer token. Left empty for a local server that needs none."},

	{Name: "HUNTDECK_POJIA_KEY", Group: GroupPojia, Label: "API key", Kind: KindSecret,
		Help: "From cve.pojia.ai. Fuels the disclosed-history panel on a program page."},
	{Name: "HUNTDECK_POJIA_URL", Group: GroupPojia, Label: "Endpoint", Kind: KindText,
		Default: "https://cve.pojia.ai/mcp",
		Help:    "Its MCP endpoint; leave at the default unless you run your own."},

	{Name: "HUNTDECK_SCAN_URL", Group: GroupScan, Label: "Endpoint", Kind: KindText,
		Help: "A ScopeSentry instance, e.g. http://10.0.8.4:8082. The handoff calls its MCP endpoint."},
	{Name: "HUNTDECK_SCAN_KEY", Group: GroupScan, Label: "MCP key", Kind: KindSecret,
		Help: "An ssk_ key scoped to task:write, task:read, node:read and template:read."},
	{Name: "HUNTDECK_JEV_KEY", Group: GroupJev, Label: "API key", Kind: KindSecret,
		Help: "From typesafe.ai. Asked before a scan handoff; a block or confirm answer stands it down."},
	{Name: "HUNTDECK_JEV_MODEL", Group: GroupJev, Label: "Model", Kind: KindText,
		Default: "jev-latest",
		Help:    "jev-latest, or jev-preview for the next version."},
	{Name: "HUNTDECK_JEV_URL", Group: GroupJev, Label: "Endpoint", Kind: KindText,
		Default: "https://api.typesafe.ai",
		Help:    "Leave at the default unless you proxy it."},
	{Name: "HUNTDECK_DECISION_PROVIDER", Group: GroupJev, Label: "Provider", Kind: KindText,
		Default: "typesafe",
		Help:    "typesafe, bailian, cloudflare or openai. The first three speak the same protocol; only the address and credentials differ."},
	{Name: "HUNTDECK_CF_ACCOUNT_ID", Group: GroupJev, Label: "Cloudflare account", Kind: KindText,
		Help: "Only for the cloudflare provider: the account whose Workers AI runs clef-flash."},
	{Name: "HUNTDECK_BAILIAN_WORKSPACE_ID", Group: GroupJev, Label: "Bailian workspace", Kind: KindText,
		Help: "Only for the bailian provider: the workspace id sent with each request."},
	{Name: "HUNTDECK_JEV_PROXY", Group: GroupJev, Label: "Proxy", Kind: KindText,
		Help: "An HTTP proxy the call leaves through, for a host whose direct egress TypeSafe refuses by region."},
}

// EditableField returns the editable field called name.
func EditableField(name string) (Field, bool) {
	for _, f := range Editable {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Overlay returns a Source in which saved values (by variable name; "" for
// one cleared in Settings) take precedence over src. A saved value hides
// the variable's NAME_FILE and aliases, so they don't clash with it.
func (s Source) Overlay(saved map[string]string) Source {
	if len(saved) == 0 {
		return s
	}
	hidden := map[string]bool{}
	for name := range saved {
		hidden[name+"_FILE"] = true
		if f, ok := EditableField(name); ok {
			for _, a := range f.Aliases {
				hidden[a], hidden[a+"_FILE"] = true, true
			}
		}
	}
	return Source{
		LookupEnv: func(name string) (string, bool) {
			if v, ok := saved[name]; ok {
				return v, true
			}
			if hidden[name] {
				return "", false
			}
			return s.LookupEnv(name)
		},
		ReadFile: s.ReadFile,
	}
}

// Lookup returns the field's value in src (from NAME, NAME_FILE or an
// alias) and whether it's set to anything. A file that can't be read
// counts as unset; Load reports it.
func (s Source) Lookup(f Field) (string, bool) {
	for _, name := range append([]string{f.Name}, f.Aliases...) {
		r := resolver{src: s}
		if v := r.get(name, ""); v != "" && len(r.errs) == 0 {
			return v, true
		}
	}
	return "", false
}
