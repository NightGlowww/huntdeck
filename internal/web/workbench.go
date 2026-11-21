package web

import "net/http"

type workbenchCard struct {
	Title, Description, URL, Action, ActionURL, Status, Icon string
}

type workbenchData struct {
	Title, Description string
	Cards              []workbenchCard
}

func (s *server) systemPage(w http.ResponseWriter, r *http.Request) {
	d := workbenchData{
		Title:       "System workbench",
		Description: "Configure the runtime, credentials and operating preferences from one place.",
		Cards: []workbenchCard{
			{Title: "Polling", Description: "Platforms, schedules, sign-in state and recent runs.", URL: "/settings", Action: "Open polling", ActionURL: "/settings", Status: "Ready", Icon: "refresh"},
			{Title: "Accounts", Description: "Encrypted credentials and environment overrides.", URL: "/settings?tab=accounts", Action: "Manage accounts", ActionURL: "/settings?tab=accounts", Status: "Review", Icon: "file"},
			{Title: "Browser", Description: "Website sessions, login tabs and the optional live view.", URL: "/settings?tab=browser", Action: "Open browser", ActionURL: "/settings?tab=browser", Status: "On demand", Icon: "monitor"},
			{Title: "Notifications", Description: "Delivery channels, events and recent deliveries.", URL: "/settings/notifications", Action: "Manage notifications", ActionURL: "/settings/notifications", Status: "Ready", Icon: "bell"},
			{Title: "Interface", Description: "Timezone, text size, theme and browser preferences.", URL: "/settings?tab=display", Action: "Customize interface", ActionURL: "/settings?tab=display", Status: "Ready", Icon: "sliders"},
			{Title: "Scoring", Description: "HuntScore weighting and the ranking strategy.", URL: "/settings?tab=you", Action: "Tune scoring", ActionURL: "/settings?tab=you", Status: "Ready", Icon: "chart"},
		},
	}
	s.render(w, r, http.StatusOK, "workbench", pageData{Title: d.Title, Nav: "system", Data: d})
}

func (s *server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	d := workbenchData{
		Title:       "Integrations workbench",
		Description: "Connect HuntDeck to scripts, agents, scanners and research services.",
		Cards: []workbenchCard{
			{Title: "API", Description: "Read data, export scope and test authenticated requests.", URL: "/api", Action: "Open API", ActionURL: "/api", Status: "Ready", Icon: "code"},
			{Title: "MCP", Description: "Connect desktop agents over HTTP or stdio.", URL: "/mcp", Action: "Open MCP", ActionURL: "/mcp", Status: "Ready", Icon: "plug"},
			{Title: "POJIA", Description: "Search disclosed reports and discovery paths.", URL: "/pojia", Action: "Open POJIA", ActionURL: "/pojia", Status: "Configured", Icon: "file"},
			{Title: "Scan handoff", Description: "Send new targets to ScopeSentry with task history.", URL: "/scan", Action: "Open scanner", ActionURL: "/scan", Status: "Review", Icon: "target"},
			{Title: "AI policy extraction", Description: "Read policy text once and cache the rules it states.", URL: "/llm", Action: "Open model", ActionURL: "/llm", Status: "Optional", Icon: "spark"},
		},
	}
	s.render(w, r, http.StatusOK, "workbench", pageData{Title: d.Title, Nav: "integrations", Data: d})
}
