package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/mdpsec/huntdeck/internal/mcpserver"
)

type workbenchCard struct {
	Title, Description, URL, Action, ActionURL, Status, Icon string
}

type workbenchData struct {
	Title, Description string
	Cards              []workbenchCard
	Metrics            []workbenchMetric
	Notices            []workbenchNotice
}

type workbenchMetric struct{ Label, Value, Hint string }
type workbenchNotice struct{ Tone, Title, Description, URL, Action string }

func workbenchSummary(ctx context.Context, s *server) (open, programs int) {
	rows, err := s.Store.Summary(ctx)
	if err != nil {
		return 0, 0
	}
	for _, row := range rows {
		open += row.Open
		programs += row.Programs
	}
	return open, programs
}

func (s *server) systemPage(w http.ResponseWriter, r *http.Request) {
	open, programs := workbenchSummary(r.Context(), s)
	pollingStatus := "Ready"
	if len(s.Platforms) == 0 {
		pollingStatus = "Review"
	}
	accountsStatus := "Ready"
	if s.Accounts == nil {
		accountsStatus = "Not configured"
	}
	browserStatus := "On demand"
	if s.hasBrowser() {
		browserStatus = "Available"
	}
	d := workbenchData{
		Title:       "System workbench",
		Description: "Configure the runtime, credentials and operating preferences from one place.",
		Cards: []workbenchCard{
			{Title: "Polling", Description: "Platforms, schedules, sign-in state and recent runs.", URL: "/settings", Action: "Open polling", ActionURL: "/settings", Status: pollingStatus, Icon: "refresh"},
			{Title: "Accounts", Description: "Encrypted credentials and environment overrides.", URL: "/settings?tab=accounts", Action: "Manage accounts", ActionURL: "/settings?tab=accounts", Status: accountsStatus, Icon: "file"},
			{Title: "Browser", Description: "Website sessions, login tabs and the optional live view.", URL: "/settings?tab=browser", Action: "Open browser", ActionURL: "/settings?tab=browser", Status: browserStatus, Icon: "monitor"},
			{Title: "Notifications", Description: "Delivery channels, events and recent deliveries.", URL: "/settings/notifications", Action: "Manage notifications", ActionURL: "/settings/notifications", Status: "Ready", Icon: "bell"},
			{Title: "Interface", Description: "Timezone, text size, theme and browser preferences.", URL: "/settings?tab=display", Action: "Customize interface", ActionURL: "/settings?tab=display", Status: "Ready", Icon: "sliders"},
			{Title: "Scoring", Description: "HuntScore weighting and the ranking strategy.", URL: "/settings?tab=you", Action: "Tune scoring", ActionURL: "/settings?tab=you", Status: "Ready", Icon: "chart"},
		},
	}
	d.Metrics = []workbenchMetric{{Label: "Platforms", Value: strconv.Itoa(len(s.Platforms)), Hint: "configured sources"}, {Label: "Programs", Value: strconv.Itoa(programs), Hint: "stored programs"}, {Label: "Open", Value: strconv.Itoa(open), Hint: "accepting reports"}}
	if len(s.Platforms) == 0 {
		d.Notices = append(d.Notices, workbenchNotice{Tone: "warn", Title: "No platform is configured", Description: "Add an account before scheduling polls.", URL: "/settings?tab=accounts", Action: "Open accounts"})
	}
	s.render(w, r, http.StatusOK, "workbench", pageData{Title: d.Title, Nav: "system", Data: d})
}

func (s *server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	pojiaStatus := "Not configured"
	if s.PojiaKey.Reveal() != "" {
		pojiaStatus = "Configured"
	}
	scanStatus := "Not configured"
	if s.ScanKey.Reveal() != "" {
		scanStatus = "Configured"
	}
	aiStatus := "Optional"
	if s.AI != nil {
		aiStatus = "Ready"
	}
	d := workbenchData{
		Title:       "Integrations workbench",
		Description: "Connect HuntDeck to scripts, agents, scanners and research services.",
		Cards: []workbenchCard{
			{Title: "API", Description: "Read data, export scope and test authenticated requests.", URL: "/api", Action: "Open API", ActionURL: "/api", Status: "Ready", Icon: "code"},
			{Title: "MCP", Description: "Connect desktop agents over HTTP or stdio.", URL: "/mcp", Action: "Open MCP", ActionURL: "/mcp", Status: "Ready", Icon: "plug"},
			{Title: "POJIA", Description: "Search disclosed reports and discovery paths.", URL: "/pojia", Action: "Open POJIA", ActionURL: "/pojia", Status: pojiaStatus, Icon: "file"},
			{Title: "Scan handoff", Description: "Send new targets to ScopeSentry with task history.", URL: "/scan", Action: "Open scanner", ActionURL: "/scan", Status: scanStatus, Icon: "target"},
			{Title: "AI policy extraction", Description: "Read policy text once and cache the rules it states.", URL: "/llm", Action: "Open model", ActionURL: "/llm", Status: aiStatus, Icon: "spark"},
		},
	}
	d.Metrics = []workbenchMetric{{Label: "MCP tools", Value: strconv.Itoa(len(mcpserver.Catalog())), Hint: "available to clients"}, {Label: "POJIA", Value: pojiaStatus, Hint: "research index"}, {Label: "Scanner", Value: scanStatus, Hint: "handoff connection"}}
	if scanStatus == "Not configured" {
		d.Notices = append(d.Notices, workbenchNotice{Tone: "info", Title: "Scanner handoff is optional", Description: "Configure ScopeSentry when you want new targets sent automatically.", URL: "/scan", Action: "Review scanner"})
	}
	s.render(w, r, http.StatusOK, "workbench", pageData{Title: d.Title, Nav: "integrations", Data: d})
}
