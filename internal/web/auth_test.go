package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/i18n"
	"github.com/mdpsec/huntdeck/internal/notify"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestAuthTokenLifecycle(t *testing.T) {
	a := newAuth("correct horse battery staple")
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	tok := a.token()
	if !a.valid(tok) {
		t.Fatal("a fresh token is not valid")
	}
	if a.valid("") || a.valid("garbage") || a.valid(tok+"x") {
		t.Error("a malformed or tampered token is valid")
	}
	payload, sig, _ := strings.Cut(tok, ".")
	if a.valid(payload + "." + strings.Repeat("A", len(sig))) {
		t.Error("a token with another signature is valid")
	}
	// Another password signs it out.
	if newAuth("another password").valid(tok) {
		t.Error("a token outlives its password")
	}
	// 30 days is the lifetime.
	now = now.Add(sessionLifetime - time.Minute)
	if !a.valid(tok) {
		t.Error("a token expired a minute early")
	}
	now = now.Add(2 * time.Minute)
	if a.valid(tok) {
		t.Error("a token older than its lifetime is valid")
	}
}

func TestAuthPasswordCheck(t *testing.T) {
	a := newAuth("correct horse battery staple")
	if !a.check("correct horse battery staple") {
		t.Error("the right password is refused")
	}
	for _, wrong := range []string{"", "Correct horse battery staple", "correct horse battery stapl", "x"} {
		if a.check(wrong) {
			t.Errorf("wrong password %q accepted", wrong)
		}
	}
}

func TestAuthLoginBudget(t *testing.T) {
	a := newAuth("password1234")
	a.now = func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }
	for range loginBudget {
		if a.tooMany("192.0.2.9") {
			t.Fatal("out of budget before trying")
		}
		a.failed("192.0.2.9")
	}
	if !a.tooMany("192.0.2.9") {
		t.Error("no budget left after the last try, but not counted")
	}
	if a.tooMany("192.0.2.10") {
		t.Error("one address's failures block another")
	}
	a.succeeded("192.0.2.9")
	if a.tooMany("192.0.2.9") {
		t.Error("a good password doesn't clear the failures")
	}
}

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                      "/",
		"/programs?q=x":         "/programs?q=x",
		"https://evil.example/": "/",
		"//evil.example/":       "/",
		"javascript:alert(1)":   "/",
		"login":                 "/",
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPasswordGate(t *testing.T) {
	const password = "correct horse battery staple"
	e := newTestEnv(t, func(d *Deps) { d.Password = password })

	// Signed out: pages redirect to the login page, the API says 401.
	res, _ := e.do(e.client(), "GET", "/", nil, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next=%2F" {
		t.Errorf("GET / = %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = e.do(e.client(), "GET", "/api/v1/status", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("API without a session = %d", res.StatusCode)
	}
	res, body := e.do(e.client(), "GET", "/login", nil, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Sign in") {
		t.Errorf("login page = %d, body %q", res.StatusCode, body)
	}

	// A wrong password is refused and sets no session.
	c := e.client()
	res, body = e.do(c, "POST", "/login", url.Values{"password": {"wrong"}, "next": {"/"}}, nil)
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Wrong password") {
		t.Errorf("wrong password = %d", res.StatusCode)
	}
	if len(res.Cookies()) != 0 {
		t.Error("a wrong password set a cookie")
	}

	// The right one signs in and the session carries.
	jar, _ := cookiejar.New(nil)
	c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ = e.do(c, "POST", "/login", url.Values{"password": {password}, "next": {"/programs"}}, nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/programs" {
		t.Fatalf("login = %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = e.do(c, "GET", "/programs", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("signed-in GET /programs = %d", res.StatusCode)
	}
	// Logging out closes the door again.
	e.do(c, "POST", "/logout", url.Values{}, nil)
	res, _ = e.do(c, "GET", "/programs", nil, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("after signing out = %d", res.StatusCode)
	}
}

func TestPasswordGateRedirectsOnlyHere(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.Password = "correct horse battery staple" })
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ := e.do(c, "POST", "/login", url.Values{"password": {"correct horse battery staple"}, "next": {"https://evil.example/"}}, nil)
	if loc := res.Header.Get("Location"); loc != "/" {
		t.Errorf("a foreign next sent the browser to %q", loc)
	}
}

func TestNoPasswordMeansNoLogin(t *testing.T) {
	e := newTestEnv(t)
	res, body := e.do(e.client(), "GET", "/", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / without a password = %d", res.StatusCode)
	}
	if strings.Contains(body, "auth-card") {
		t.Error("the login page showed without a password set")
	}
}

func TestLanguageByHeaderAndCookie(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	h := http.Header{"Accept-Language": {"zh-CN,zh;q=0.9"}}
	_, body := e.do(c, "GET", "/programs", nil, h)
	if !strings.Contains(body, "狩猎面板") || strings.Contains(body, ">Hunt board<") {
		t.Error("a Chinese browser didn't get the Chinese interface")
	}
	if !strings.Contains(body, "<html lang=\"zh\"") {
		t.Error("the page's lang attribute isn't the page's language")
	}
	// Choosing English keeps English.
	res, _ := e.do(c, "POST", "/settings/language", url.Values{"lang": {"en"}, "next": {"/programs"}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("language switch = %d", res.StatusCode)
	}
	var langCookie *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == langCookieName {
			langCookie = ck
		}
	}
	if langCookie == nil || langCookie.Value != "en" {
		t.Fatalf("no language cookie: %+v", res.Cookies())
	}
	req := http.Header{"Accept-Language": {"zh-CN"}, "Cookie": {"hd_lang=en"}}
	_, body = e.do(c, "GET", "/programs", nil, req)
	if !strings.Contains(body, ">Hunt board<") {
		t.Error("the cookie didn't override the browser's language")
	}
}
func TestChineseDashboard(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	res, body := e.do(e.client(), "GET", "/", nil, h)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", res.StatusCode)
	}
	for _, want := range []string{"狩猎面板", "最新攻击面", "在架项目", "私有邀请", "全部平台", "精选推荐"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese dashboard lacks %q", want)
		}
	}
	for _, unwanted := range []string{">Hunt board<", ">Fresh attack surface<", ">Top picks<", ">Open programs<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the Chinese dashboard still shows %q", unwanted)
		}
	}
	// A page title is a message too.
	if !strings.Contains(body, "<title>狩猎面板 · HuntDeck</title>") {
		t.Error("page title not translated")
	}
}
func TestChineseProgramsPage(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/programs", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"视图", "最值得挖", "赏金", "攻击面", "门槛", "自定义"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese Programs page lacks %q", want)
		}
	}
	for _, unwanted := range []string{">Best to hunt<", ">Attack surface<", ">Requirements<", ">Customize<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the Chinese Programs page still shows %q", unwanted)
		}
	}
}
func TestChineseChangesAndTargets(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, changes := e.do(e.client(), "GET", "/changes", nil, h)
	for _, want := range []string{"时间范围", "全部时间", "详情", "排序：", "条变更"} {
		if !strings.Contains(changes, want) {
			t.Errorf("the Chinese Changes page lacks %q", want)
		}
	}
	if strings.Contains(changes, ">All time<") || strings.Contains(changes, ">Detail<") {
		t.Error("the Chinese Changes page still shows English labels")
	}
	_, targets := e.do(e.client(), "GET", "/targets", nil, h)
	for _, want := range []string{"最佳新目标", "范围内", "新增时间", "目标评分"} {
		if !strings.Contains(targets, want) {
			t.Errorf("the Chinese Targets page lacks %q", want)
		}
	}
}

func TestChineseChangeSentences(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		c    store.ChangeView
		want string
	}{
		{store.ChangeView{Kind: store.ChangeProgramAdded, NewValue: "public"}, "新的公开项目"},
		{store.ChangeView{Kind: store.ChangeStateChanged, OldValue: "open", NewValue: "paused"}, "状态：在架 → 已暂停"},
		{store.ChangeView{Kind: store.ChangeScopeAdded, NewValue: "*.example.com"}, "范围新增：*.example.com"},
		{store.ChangeView{Kind: store.ChangeScopeAdded, Field: "out_of_scope", NewValue: "admin.example.com"}, "范围新增（范围外）：admin.example.com"},
	}
	for _, tt := range tests {
		if got := describeChangeLang("zh", m, tt.c); got != tt.want {
			t.Errorf("describeChangeLang(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
	if got := changeTagLang("zh", m, store.ChangeScopeAdded); got != "+ 范围" {
		t.Errorf("changeTag = %q", got)
	}
}
func TestChineseProgramPage(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/programs/fake/acme", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"概览", "范围", "政策", "统计", "变更", "笔记", "赏金表", "测试之前", "最近变更", "评分原因", "范围内资产"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese program page lacks %q", want)
		}
	}
	for _, unwanted := range []string{">Overview<", ">Before you test<", ">Bounty table<", ">Recent changes<", ">Why it scores<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the Chinese program page still shows %q", unwanted)
		}
	}
}
func TestChineseProgramTabs(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, scope := e.do(e.client(), "GET", "/programs/fake/acme?tab=scope", nil, h)
	for _, want := range []string{"资产", "类型", "备注", "筛选资产与备注"} {
		if !strings.Contains(scope, want) {
			t.Errorf("the Chinese Scope tab lacks %q", want)
		}
	}
	_, policy := e.do(e.client(), "GET", "/programs/fake/acme?tab=policy", nil, h)
	if !strings.Contains(policy, "目录") {
		t.Error("the Chinese Policy tab lacks 目录")
	}
	_, notes := e.do(e.client(), "GET", "/programs/fake/acme?tab=notes", nil, h)
	if !strings.Contains(notes, "保存笔记") {
		t.Error("the Chinese Notes tab lacks its save button")
	}
}
func TestChineseSettingsAndNotifications(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, settings := e.do(e.client(), "GET", "/settings", nil, h)
	for _, want := range []string{"轮询", "显示", "平台", "启用", "立即轮询"} {
		if !strings.Contains(settings, want) {
			t.Errorf("the Chinese Settings page lacks %q", want)
		}
	}
	if strings.Contains(settings, ">Polling<") || strings.Contains(settings, ">Platform<") {
		t.Error("the Chinese Settings page still shows English labels")
	}
	_, notes := e.do(e.client(), "GET", "/settings/notifications", nil, h)
	for _, want := range []string{"渠道", "消息里的链接", "消息示例", "添加 Discord"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the Chinese Notifications page lacks %q", want)
		}
	}
}
func TestChineseDisclosedMatchAPI(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, disc := e.do(e.client(), "GET", "/disclosed", nil, h)
	for _, want := range []string{"已披露报告", "报告", "赞同", "最高赏金"} {
		if !strings.Contains(disc, want) {
			t.Errorf("the Chinese Disclosed page lacks %q", want)
		}
	}
	_, match := e.do(e.client(), "GET", "/match?q=acme.example", nil, h)
	for _, want := range []string{"检查", "规则", "在范围内"} {
		if !strings.Contains(match, want) {
			t.Errorf("the Chinese scope check lacks %q", want)
		}
	}
	_, api := e.do(e.client(), "GET", "/api", nil, h)
	for _, want := range []string{"快速开始", "地址", "分页", "发送"} {
		if !strings.Contains(api, want) {
			t.Errorf("the Chinese API page lacks %q", want)
		}
	}
}
func TestChineseMethodologyPage(t *testing.T) {
	e := newTestEnv(t)
	_, zh := e.do(e.client(), "GET", "/methodology", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"一览", "公开、统计与估算", "累计支付", "HuntScore", "API 中：估算与新鲜度"} {
		if !strings.Contains(zh, want) {
			t.Errorf("the Chinese Methodology page lacks %q", want)
		}
	}
	if strings.Contains(zh, "At a glance") || strings.Contains(zh, "Published, counted and estimated") {
		t.Error("the Chinese Methodology page still shows the English text")
	}
	_, en := e.do(e.client(), "GET", "/methodology", nil, nil)
	if !strings.Contains(en, "At a glance") {
		t.Error("the English Methodology page lost its text")
	}
}
func TestChineseStatsPages(t *testing.T) {
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, stats := e.do(e.client(), "GET", "/stats", nil, h)
	for _, want := range []string{"按平台", "开放项目", "范围内资产", "赏金", "全部平台", "上升", "下降", "平台不公开该数字"} {
		if !strings.Contains(stats, want) {
			t.Errorf("the Chinese Stats page lacks %q", want)
		}
	}
	if strings.Contains(stats, ">By platform<") || strings.Contains(stats, ">All platforms<") {
		t.Error("the Chinese Stats page still shows English labels")
	}
	_, plat := e.do(e.client(), "GET", "/stats/fake", nil, h)
	for _, want := range []string{"统计", "开放项目", "筛选"} {
		if !strings.Contains(plat, want) {
			t.Errorf("the Chinese platform stats page lacks %q", want)
		}
	}
}
func TestChineseAccountsFields(t *testing.T) {
	// The Accounts tab needs saved-settings support the test environment
	// doesn't build, so check its two moving parts: the form renders labels
	// and help through T, and the catalog has their Chinese.
	partial, err := fs.ReadFile(templateFS, "templates/partials/accounts.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"{{T .Label}}", "{{T .}}"} {
		if !strings.Contains(string(partial), want) {
			t.Errorf("accounts.html doesn't render %s through T", want)
		}
	}
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"API username", "Requests per second", "Parallel scope fetches", "not set up"} {
		if got := m.T("zh", key); got == key {
			t.Errorf("the catalog has no Chinese for %q", key)
		}
	}
}
func TestChineseChannelForm(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/settings/notifications/new?kind=telegram", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"发送到哪里", "机器人令牌", "什么时候发", "发送什么", "涉及哪些项目", "添加 Telegram 渠道"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese channel form lacks %q", want)
		}
	}
	if strings.Contains(body, ">Bot token<") || strings.Contains(body, ">Where<") {
		t.Error("the channel form still shows English labels")
	}
}
func TestChineseAPIPage(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/api", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{"列出项目", "检查单个主机", "狩猎面板的汇总", "项目列表、单个项目的完整信息"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Chinese API page lacks %q", want)
		}
	}
	if strings.Contains(body, ">List programs<") {
		t.Error("the API page still shows English summaries")
	}
	// The spec served to machines stays English.
	_, spec := e.do(e.client(), "GET", "/api/v1/openapi.json", nil, nil)
	if !strings.Contains(spec, "List programs") {
		t.Error("the served OpenAPI description lost its English")
	}
}
func TestChineseNotificationCards(t *testing.T) {
	// The cards' words are Go-side (notify.CardIn), so check them directly:
	// the same events read in English for deliveries and in Chinese here.
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tr := func(msg string, args ...any) string { return m.TF("zh", msg, args...) }
	tests := []struct {
		e    notify.Event
		want string
	}{
		{notify.Event{Kind: notify.EventLaunch}, "新项目"},
		{notify.Event{Kind: notify.EventBountyRaised}, "赏金上调"},
		{notify.Event{Kind: notify.EventScopeAdded, Assets: []notify.Asset{{Identifier: "a"}, {Identifier: "b"}}}, "2 个新的范围内资产"},
		{notify.Event{Kind: notify.EventReopened}, "重新开放"},
	}
	for _, tt := range tests {
		if got := tt.e.CardIn(tr).Title; got != tt.want {
			t.Errorf("CardIn(%s).Title = %q, want %q", tt.e.Kind, got, tt.want)
		}
		if en := tt.e.Card().Title; en == tt.want {
			t.Errorf("Card() for %s should stay English", tt.e.Kind)
		}
	}
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/settings/notifications", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if !strings.Contains(body, "消息示例") {
		t.Error("the Chinese notifications page lacks 消息示例")
	}
}
func TestChineseScriptMessages(t *testing.T) {
	e := newTestEnv(t)
	_, zh := e.do(e.client(), "GET", "/i18n.js", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if !strings.Contains(zh, "\"Keyboard shortcuts\":\"键盘快捷键\"") {
		t.Error("the scripts' messages aren't served in Chinese")
	}
	_, en := e.do(e.client(), "GET", "/i18n.js", nil, nil)
	if !strings.Contains(en, "window.HD_I18N = null") {
		t.Error("an English visitor shouldn't get a Chinese catalog")
	}
}

// Every message the scripts ask for has Chinese, so none falls back to
// English by accident.
func TestScriptMessagesCovered(t *testing.T) {
	m := i18n.JS("zh")
	if len(m) == 0 {
		t.Fatal("no script catalog")
	}
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		t.Fatal(err)
	}
	asked := map[string]string{}
	re := regexp.MustCompile(`\bt\(\s*"([^"]+)"`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, err := fs.ReadFile(staticFS, "static/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, mm := range re.FindAllStringSubmatch(string(b), -1) {
			asked[mm[1]] = e.Name()
		}
	}
	for msg, file := range asked {
		if _, ok := m[msg]; !ok {
			t.Errorf("%s asks for %q, which the script catalog doesn't have", file, msg)
		}
	}
}
func TestChineseHuntReasons(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ in, want string }{
		{"No new scope in the last 90 days", "近 90 天没有新增范围"},
		{"Running since 2013: well picked over", "自 2013 年运营至今，已被充分挖掘"},
		{"No bounties (disclosure program)", "无赏金（仅披露项目）"},
		{"100% on time", "100% 按时"},
		{"Most reports validated within 26d", "多数报告在 26d 内完成验证"},
		{"First response in about 1d 17h", "首次响应约 1d 17h"},
		{"Average bounty $574 (top 22% on HackerOne)", "平均赏金 $574（HackerOne 前 22%）"},
		{"Bounties up to $7.5k (top 19% on Bugcrowd)", "最高赏金 $7.5k（Bugcrowd 前 19%）"},
		{"$54k paid in the last 90 days (top 20% on HackerOne)", "$54k 支出（近 90 天，HackerOne 前 20%）"},
		{"1067 reports in 90 days for 26 in-scope assets: busier than 93% of HackerOne programs", "90 天 1067 份报告 / 26 个范围内资产：比 HackerOne 的 93% 项目更活跃"},
		{"0 reports in 90 days for 0 in-scope assets: quieter than 89% of HackerOne programs", "90 天 0 份报告 / 0 个范围内资产：比 HackerOne 的 89% 项目更冷清"},
		{"27 accepted reports in 90 days for 359 in-scope assets: busier than 70% of Bugcrowd programs", "90 天 27 份已接受报告 / 359 个范围内资产：比 Bugcrowd 的 70% 项目更活跃"},
		{"Bugcrowd doesn't share report volume", "Bugcrowd 不公开报告量"},
		{"Private: invite-only, far fewer hunters", "私有：仅限邀请，竞争者少得多"},
		{"Launched 3 months ago: not picked over yet", "3 个月前上线：尚未被充分挖掘"},
		{"Invited 2 days ago", "2 天前收到邀请"},
	}
	for _, tt := range tests {
		if got := reasonText("zh", m, tt.in); got != tt.want {
			t.Errorf("reasonText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// A shape it doesn't know stays as it is.
	if got := reasonText("zh", m, "something new"); got != "something new" {
		t.Errorf("unknown shape = %q", got)
	}
	// English pages keep the stored sentence.
	if got := reasonText("en", m, "Private: invite-only, far fewer hunters"); got != "Private: invite-only, far fewer hunters" {
		t.Errorf("English reason changed: %q", got)
	}
}
func TestChineseTargetReasons(t *testing.T) {
	m, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ in, want string }{
		{"Domain added 8 hours ago", "域名 新增于 8 小时前"},
		{"Wildcard new to you: in scope when you joined 1 day ago", "通配符 对你而言是新的：加入时已在范围内（1 天前）"},
		{"Program pays no bounties", "项目不付赏金"},
		{"Bounty-eligible up to critical", "可获赏金，最高 严重"},
		{"Root domain new to the program", "根域对该项目是新的"},
		{"No other program lists it", "没有其他项目列出它"},
		{"Also listed by 3 other programs", "另有 3 个项目也列出"},
		{"Program HuntScore 46 (competition 1, payout 8, response 4)", "项目 HuntScore 46（竞争 1，赏金 8，响应 4）"},
	}
	for _, tt := range tests {
		if got := reasonText("zh", m, tt.in); got != tt.want {
			t.Errorf("reasonText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// A target's tooltip joins them the same way.
	got := reasonsText("zh", m, []string{"Domain added 1 day ago", "Bounty-eligible"})
	if got != "域名 新增于 1 天前 · 可获赏金" {
		t.Errorf("reasonsText = %q", got)
	}
}
func TestChineseRequirementTitles(t *testing.T) {
	if got := reqTitle("zh", "1.0 or higher past-year Signal on HackerOne; below it you get trial reports only"); got != "过去一年 Signal 需 ≥ 1.0（HackerOne）；低于此仅有试用报告" {
		t.Errorf("signal title = %q", got)
	}
	if got := reqTitle("zh", "Identity-verified hackers only (ID verification on Intigriti)"); got != "仅限通过身份验证的研究者（在 Intigriti 完成身份验证）" {
		t.Errorf("id title = %q", got)
	}
	if got := reqTitle("en", "Background-cleared hackers only"); got != "Background-cleared hackers only" {
		t.Errorf("English title changed: %q", got)
	}
}
func TestChineseFeedbackAndDashboardHints(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) { d.FeedbackEmail = "hello@example.com" })
	h := http.Header{"Accept-Language": {"zh-CN"}}
	res, fb := e.do(e.client(), "GET", "/feedback?page=/programs", nil, h)
	if !strings.Contains(fb, "写信给我们") || strings.Contains(fb, "Write to us") {
		t.Errorf("the feedback page isn't in Chinese: %d %.200s", res.StatusCode, fb)
	}
	_, dash := e.do(e.client(), "GET", "/", nil, h)
	for _, want := range []string{"拖动卡片左上角的手柄", "适应内容", "近 7 天内"} {
		if !strings.Contains(dash, want) {
			t.Errorf("the dashboard lacks %q", want)
		}
	}
}
func TestChineseChartLabels(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Jan", "1月"},
		{"Oct", "10月"},
		{"Jan 2", "1月2日"},
		{"Nov 17, 2026", "2026年11月17日"},
		{"9mo 1w", "9 个月 1 周"},
		{"1y 3mo", "1 年 3 个月"},
		{"13d 22h", "13 天 22 小时"},
		{"8d", "8 天"},
		{"2w", "2 周"},
	}
	for _, tt := range tests {
		if got := figText("zh", tt.in); got != tt.want {
			t.Errorf("figText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := figText("en", "Jan 2"); got != "Jan 2" {
		t.Errorf("English label changed: %q", got)
	}
	if got := figText("zh", "something else"); got != "something else" {
		t.Errorf("unknown label changed: %q", got)
	}
}

func TestAccessibilityBits(t *testing.T) {
	// Without a password the pages render straight away; the login page
	// needs one, so it gets its own instance.
	e := newTestEnv(t)
	h := http.Header{"Accept-Language": {"zh-CN"}}
	_, dash := e.do(e.client(), "GET", "/", nil, h)
	for _, want := range []string{`class="skip-link"`, `id="main"`, "跳到主要内容", "data-board-live"} {
		if !strings.Contains(dash, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	_, programs := e.do(e.client(), "GET", "/programs", nil, h)
	if !strings.Contains(programs, `role="status" aria-live="polite"`) {
		t.Error("the list total isn't a live region")
	}
	e2 := newTestEnv(t, func(d *Deps) { d.Password = "local-dev-password" })
	_, login := e2.do(e2.client(), "GET", "/login", nil, h)
	for _, want := range []string{`id="pw-caps"`, "大写锁定已开启"} {
		if !strings.Contains(login, want) {
			t.Errorf("the login page lacks %q", want)
		}
	}
}

func TestTargetsSelection(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/targets", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{`data-select-all`, `class="pick"`, `data-selbar`, "复制所选"} {
		if !strings.Contains(body, want) {
			t.Errorf("the targets list lacks %q", want)
		}
	}
}

func TestDisclosedSelection(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/disclosed", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{`data-select-all`, `class="pick"`, `id="disclosed-list"`, "复制所选链接"} {
		if !strings.Contains(body, want) {
			t.Errorf("the disclosed list lacks %q", want)
		}
	}
}

func TestProgramsSelection(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), "GET", "/programs", nil, http.Header{"Accept-Language": {"zh-CN"}})
	for _, want := range []string{`data-select-all`, `class="pick"`, `id="programs-list"`, "复制所选项目",
		`data-selcount role="status" aria-live="polite"`, `aria-label="全选"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the programs list lacks %q", want)
		}
	}
}

func TestBulkIgnore(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	form := url.Values{"p": {"fake/acme", "fake/missing"}, "ignored": {"1"}, "back": {"/programs?state=all"}}
	res, _ := e.do(c, "POST", "/programs/ignore", form, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("bulk ignore = %d, want 303", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/programs?state=all") {
		t.Errorf("redirect = %q", loc)
	}
	_, body := e.do(c, "GET", "/api/v1/programs/fake/acme", nil, nil)
	var got struct {
		Ignored bool `json:"ignored"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Ignored {
		t.Error("the program wasn't ignored")
	}
	if res, _ := e.do(c, "POST", "/programs/ignore", url.Values{"ignored": {"1"}}, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty selection = %d, want 400", res.StatusCode)
	}
}

func TestProgramsSortAria(t *testing.T) {
	e := newTestEnv(t)
	cases := []struct{ query, want string }{
		{"", "descending"},          // the default, HuntScore, biggest first
		{"?sort=name", "ascending"}, // names A to Z
		{"?sort=name&rev=1", "descending"},
		{"?sort=paidall", "descending"},
	}
	for _, c := range cases {
		_, body := e.do(e.client(), "GET", "/programs"+c.query, nil, nil)
		if got := strings.Count(body, "aria-sort="); got != 1 {
			t.Errorf("%q: %d headers carry aria-sort, want 1", c.query, got)
		}
		if !strings.Contains(body, `aria-sort="`+c.want+`"`) {
			t.Errorf("%q: no aria-sort=%q", c.query, c.want)
		}
	}
}

func TestTargetsAndDisclosedSortAria(t *testing.T) {
	e := newTestEnv(t)
	cases := []struct{ path, want string }{
		{"/targets", `class="c-added" aria-sort="descending"`},
		{"/targets?sort=bounty", `class="c-bounty num" aria-sort="descending"`},
		{"/disclosed", `class="c-date" aria-sort="descending"`},
		{"/disclosed?sort=votes", `class="c-votes num" aria-sort="descending"`},
	}
	for _, c := range cases {
		_, body := e.do(e.client(), "GET", c.path, nil, nil)
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: no %s", c.path, c.want)
		}
		if got := strings.Count(body, "aria-sort="); got != 1 {
			t.Errorf("%s: %d headers carry aria-sort, want 1", c.path, got)
		}
	}
}

func TestEmptyListMentionsIgnoredPrograms(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	e.do(c, "POST", "/programs/ignore", url.Values{"p": {"fake/acme"}, "ignored": {"1"}, "back": {"/programs"}}, nil)
	_, body := e.do(c, "GET", "/programs?q=zzz-nothing", nil, http.Header{"Accept-Language": {"zh-CN"}})
	if !strings.Contains(body, "另有 1 个已忽略的项目未显示") || !strings.Contains(body, "ignored=only") {
		t.Error("the empty list doesn't mention the ignored programs")
	}
}

// A distributed guess (or a proxy hiding every visitor behind one address)
// hits the global pause, and the pause lifts with the window.
func TestAuthSprayBudget(t *testing.T) {
	clock := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	a := newAuth("password1234")
	a.now = func() time.Time { return clock }

	for i := 0; i < sprayBudget; i++ {
		a.failed(fmt.Sprintf("198.51.100.%d", i))
	}
	if !a.spraying() {
		t.Fatalf("%d wrong passwords did not pause logins", sprayBudget)
	}
	if a.tooMany("203.0.113.5") {
		t.Error("a fresh address is still over its own budget")
	}

	clock = clock.Add(sprayWindow + time.Second)
	if a.spraying() {
		t.Error("the pause outlived the window")
	}
}

// One IPv6 host owns a /64: addresses inside it share one budget, so a scan
// cannot mint a fresh address per guess.
func TestBudgetKeyAggregatesIPv6(t *testing.T) {
	cases := []struct{ in, want string }{
		{"203.0.113.9", "203.0.113.9"},
		{"203.0.113.9:5555", "203.0.113.9"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff::9", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2::1]:443", "2001:db8:1:2::/64"},
		{"not-an-ip", "not-an-ip"},
	}
	for _, c := range cases {
		if got := budgetKey(c.in); got != c.want {
			t.Errorf("budgetKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The attempts map stays bounded however many addresses guess once.
func TestFailedAttemptsStayBounded(t *testing.T) {
	a := newAuth("secret")
	for i := 0; i < maxBudgets*3; i++ {
		a.failed(fmt.Sprintf("198.51.100.%d", i))
	}
	a.mu.Lock()
	n := len(a.fails)
	a.mu.Unlock()
	if n > maxBudgets {
		t.Errorf("the attempts map holds %d entries, want at most %d", n, maxBudgets)
	}
}

// An oversized login body is refused before it is parsed, and does not spend
// the address's budget: the limit is not a password attempt.
func TestLoginRefusesAnOversizedBody(t *testing.T) {
	const password = "correct horse battery staple"
	e := newTestEnv(t, func(d *Deps) { d.Password = password })
	c := e.client()
	big := strings.Repeat("x", 8<<10)
	res, _ := e.do(c, "POST", "/login", url.Values{"password": {big}, "next": {"/"}}, nil)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("an 8 KiB login body = %d, want 413", res.StatusCode)
	}
	// Still able to sign in: nothing was counted against the address.
	jar, _ := cookiejar.New(nil)
	c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ = e.do(c, "POST", "/login", url.Values{"password": {password}}, nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("sign in after an oversized body = %d", res.StatusCode)
	}
}
