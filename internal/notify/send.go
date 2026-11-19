package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sendError is a failed delivery. Its text never holds the channel's URL
// or token: both are secrets.
type sendError struct {
	msg        string
	retryAfter time.Duration // the service asked to wait this long
	permanent  bool          // retrying won't help (bad URL or token, say)
}

func (e *sendError) Error() string { return e.msg }

// post sends a JSON body and reads the reply, keeping secrets out of errors.
func (n *Notifier) post(ctx context.Context, target string, body []byte, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, &sendError{msg: "bad address", permanent: true}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HuntDeck")
	for k, v := range header {
		req.Header[k] = v
	}
	return n.do(req)
}

// do sends a prepared request and reads the reply.
func (n *Notifier) do(req *http.Request) ([]byte, error) {
	res, err := n.Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // url.Error's text includes the URL
		}
		return nil, &sendError{msg: "request failed: " + err.Error()}
	}
	defer func() { _ = res.Body.Close() }()
	reply, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return reply, nil
	}
	e := &sendError{msg: fmt.Sprintf("HTTP %d", res.StatusCode)}
	if s := strings.TrimSpace(string(reply)); s != "" {
		e.msg += ": " + truncate(strings.Join(strings.Fields(s), " "), 200)
	}
	switch res.StatusCode {
	case http.StatusTooManyRequests:
		e.retryAfter = retryAfter(res.Header.Get("Retry-After"), reply)
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		e.permanent = true
	}
	return reply, e
}

// retryAfter reads how long a service asked to wait: the Retry-After
// header (seconds), or Discord's and Telegram's JSON fields.
func retryAfter(header string, body []byte) time.Duration {
	if s, err := strconv.ParseFloat(header, 64); err == nil && s > 0 {
		return time.Duration(s * float64(time.Second))
	}
	var r struct {
		RetryAfter float64 `json:"retry_after"` // Discord
		Parameters struct {
			RetryAfter float64 `json:"retry_after"` // Telegram
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &r) == nil {
		if s := max(r.RetryAfter, r.Parameters.RetryAfter); s > 0 {
			return time.Duration(s * float64(time.Second))
		}
	}
	return time.Minute
}

func (n *Notifier) sendDiscord(ctx context.Context, c Config, text string) error {
	body, _ := json.Marshal(map[string]any{
		"content":          text,
		"username":         "HuntDeck",
		"allowed_mentions": map[string]any{"parse": []string{}}, // names from platforms never ping anyone
	})
	_, err := n.post(ctx, c.URL, body, nil)
	return err
}

func (n *Notifier) sendTelegram(ctx context.Context, c Config, text string) error {
	body, _ := json.Marshal(map[string]any{
		"chat_id":              c.ChatID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	})
	reply, err := n.post(ctx, n.TelegramAPI+"/bot"+c.Token+"/sendMessage", body, nil)
	if err != nil {
		var se *sendError
		var r struct {
			Description string `json:"description"`
		}
		if errors.As(err, &se) && json.Unmarshal(reply, &r) == nil && r.Description != "" {
			se.msg = "Telegram: " + truncate(r.Description, 200)
		}
	}
	return err
}

func (n *Notifier) sendWebhook(ctx context.Context, c Config, kind string, body []byte) error {
	h := http.Header{"X-Huntdeck-Event": {kind}}
	if c.Secret != "" {
		mac := hmac.New(sha256.New, []byte(c.Secret))
		mac.Write(body)
		h.Set("X-HuntDeck-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	_, err := n.post(ctx, c.URL, body, h)
	return err
}

// postForm sends a form body and reads the reply, keeping secrets out of
// errors (ServerChan takes url-encoded fields).
func (n *Notifier) postForm(ctx context.Context, target string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &sendError{msg: "bad address", permanent: true}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "HuntDeck")
	return n.do(req)
}

// sendBark pushes one message to an iOS device through Bark or a
// self-hosted bark-server: POST {server}/push with the device key.
func (n *Notifier) sendBark(ctx context.Context, cfg Config, text string) error {
	group := strings.TrimSpace(cfg.Group)
	if group == "" {
		group = "HuntDeck"
	}
	payload := map[string]string{
		"device_key": cfg.DeviceKey,
		"title":      "HuntDeck",
		"body":       text,
		"group":      group,
	}
	if s := strings.TrimSpace(cfg.Sound); s != "" {
		payload["sound"] = s
	}
	if l := strings.TrimSpace(cfg.Level); l != "" {
		payload["level"] = l
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &sendError{msg: "bad message", permanent: true}
	}
	reply, err := n.post(ctx, serverBase(cfg.Server, "https://api.day.app")+"/push", body, nil)
	if err != nil {
		return err
	}
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(reply, &r) == nil && r.Code != 0 && r.Code != 200 {
		return &sendError{msg: "Bark refused it: " + truncate(strings.TrimSpace(r.Message), 120), permanent: r.Code == 400}
	}
	return nil
}

// sendServerChan pushes one message to WeChat through ServerChan (Server酱):
// POST {server}/{SendKey}.send with title and desp (markdown).
func (n *Notifier) sendServerChan(ctx context.Context, cfg Config, text string) error {
	base := serverBase(cfg.Server, "https://sctapi.ftqq.com")
	form := url.Values{"title": {"HuntDeck"}, "desp": {text}, "tags": {"HuntDeck"}}
	reply, err := n.postForm(ctx, base+"/"+url.PathEscape(cfg.SendKey)+".send", form)
	if err != nil {
		return err
	}
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(reply, &r) == nil && r.Code != 0 {
		return &sendError{msg: "ServerChan refused it: " + truncate(strings.TrimSpace(r.Message), 120)}
	}
	return nil
}
