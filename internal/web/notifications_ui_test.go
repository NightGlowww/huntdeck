package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

// With a channel configured, the notifications page carries the live-delivery
// hook and a filter that works without JavaScript (the Apply button is only
// hidden by the script, never by the markup).
func TestNotificationsWithAChannel(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {})
	if _, err := e.store.SaveNotifyChannel(context.Background(), store.NotifyChannel{
		Name: "ops", Kind: "webhook", Config: `{"url":"http://127.0.0.1:9/hook"}`,
		Enabled: true, Instant: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(e.client(), http.MethodGet, "/settings/notifications", nil, nil)
	if !strings.Contains(body, `id="deliveries" data-refresh="30"`) {
		t.Error("the deliveries list has no refresh hook")
	}
	if strings.Contains(body, `data-autosubmit-button hidden`) {
		t.Error("the Apply button ships hidden: without JS the filter cannot be applied")
	}
	if !strings.Contains(body, `data-autosubmit`) {
		t.Error("the channel filter lost its auto-submit hook")
	}
}
