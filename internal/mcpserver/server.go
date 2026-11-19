// Package mcpserver answers the Model Context Protocol over stdio: a small
// set of tools over HuntDeck's REST API, so an agent can ask a running
// instance what it knows. It adds no accounts: it signs in with the
// instance's access password the way a browser does, and holds the cookie
// in memory.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// protocolVersion is what this server speaks; clients negotiate down.
const protocolVersion = "2025-06-18"

// Server is one HuntDeck instance, seen through its API.
type Server struct {
	Base     string // https://huntdeck.example, no trailing slash
	Password string // HUNTDECK_PASSWORD, when the instance has one
	Version  string // reported in initialize
	// ReadOnly hides the tools that change anything (a poll): an agent
	// that should only look gets only look-ups.
	ReadOnly bool
	HTTP     *http.Client

	jar http.CookieJar // the session, kept in memory only

	// mu guards lastPoll: the poll tool asks for at most one poll a minute,
	// so an agent that loops can't turn into a load test on the platforms.
	mu       sync.Mutex
	lastPoll map[string]time.Time // by platform: one platform's poll is no reason to refuse another's
	calls    []time.Time          // the tool calls still in the window
	now      func() time.Time
}

// New returns a server for base.
func New(base, password, version string, readOnly bool) *Server {
	jar, _ := cookiejar.New(nil)
	return &Server{
		Base:     strings.TrimRight(base, "/"),
		Password: password,
		Version:  version,
		ReadOnly: readOnly,
		jar:      jar,
		now:      time.Now,
		// Redirects are answers here, not something to follow: HuntDeck
		// sends a sessionless request to /login, and following that would
		// hand back the login page as if it were the data.
		HTTP: &http.Client{Timeout: 30 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

// request is one JSON-RPC call.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is one JSON-RPC answer.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Run reads requests line by line and writes answers, until in ends.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		res, err := s.handle(ctx, req)
		if err != nil {
			res = &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		if res == nil {
			continue // a notification: nothing to answer
		}
		res.JSONRPC = "2.0"
		if len(res.ID) == 0 {
			res.ID = req.ID
		}
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// handle answers one request, or returns nil for a notification.
func (s *Server) handle(ctx context.Context, req request) (*response, error) {
	switch req.Method {
	case "initialize":
		return &response{Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "huntdeck", "version": s.Version},
		}}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return &response{Result: map[string]any{}}, nil
	case "tools/list":
		return &response{Result: map[string]any{"tools": s.toolList()}}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("bad arguments: %w", err)
		}
		text, err := s.callTool(ctx, p.Name, p.Arguments)
		if err != nil {
			return &response{Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": err.Error()}},
				"isError": true,
			}}, nil
		}
		return &response{Result: map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
		}}, nil
	}
	return &response{Error: &rpcError{Code: -32601, Message: "unknown method " + req.Method}}, nil
}

// get asks the instance for a path, signing in again when the session has
// gone (a restart, or the password changed).
func (s *Server) get(ctx context.Context, path string, query url.Values) (string, error) {
	target := s.Base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	body, err := s.do(ctx, http.MethodGet, target, nil, "")
	if errors.Is(err, errUnauthorized) && s.Password != "" {
		if err := s.login(ctx); err != nil {
			return "", err
		}
		body, err = s.do(ctx, http.MethodGet, target, nil, "")
	}
	return body, err
}

func (s *Server) post(ctx context.Context, path string) (string, error) {
	body, err := s.do(ctx, http.MethodPost, s.Base+path, nil, "")
	if errors.Is(err, errUnauthorized) && s.Password != "" {
		if err := s.login(ctx); err != nil {
			return "", err
		}
		body, err = s.do(ctx, http.MethodPost, s.Base+path, nil, "")
	}
	return body, err
}

// postText POSTs a plain-text body (the bulk matcher reads its inputs from
// one), signing in again when the session has ended.
func (s *Server) postText(ctx context.Context, path, contentType, body string) (string, error) {
	req := func() (string, error) {
		return s.doRaw(ctx, http.MethodPost, s.Base+path, body, contentType)
	}
	out, err := req()
	if errors.Is(err, errUnauthorized) && s.Password != "" {
		if err := s.login(ctx); err != nil {
			return "", err
		}
		out, err = req()
	}
	return out, err
}

var errUnauthorized = errors.New("the instance refused the session (is HUNTDECK_PASSWORD right?)")

// do sends a form-encoded request; doRaw is the same for any body.
func (s *Server) do(ctx context.Context, method, target string, form url.Values, contentType string) (string, error) {
	body := ""
	if form != nil {
		body = form.Encode()
		if contentType == "" {
			contentType = "application/x-www-form-urlencoded"
		}
	}
	return s.doRaw(ctx, method, target, body, contentType)
}

func (s *Server) doRaw(ctx context.Context, method, target, body, contentType string) (string, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return "", err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("%s %s: %w", method, target, err)
	}
	defer func() { _ = res.Body.Close() }()
	reply, capErr := readCapped(res.Body, maxReply)
	switch {
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		return "", errUnauthorized
	case res.StatusCode >= 300 && res.StatusCode < 400 && strings.Contains(res.Header.Get("Location"), "/login"):
		return "", errUnauthorized
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return "", fmt.Errorf("%s %s: redirected to %s", method, target, res.Header.Get("Location"))
	case res.StatusCode >= 400:
		return "", fmt.Errorf("%s %s: HTTP %d: %s", method, target, res.StatusCode, strings.TrimSpace(string(reply)))
	}
	if capErr != nil {
		return "", fmt.Errorf("%s %s: %w", method, target, capErr)
	}
	return string(reply), nil
}

// maxReply caps one answer. A longer one is an error, never a quiet cut: an
// agent that saves a truncated Burp or Caido file has a file that will not
// import, and nothing in it would say so.
const maxReply = 8 << 20

// readCapped reads at most max bytes and fails when there is more.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return body, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("the answer is over %d MiB: ask for less — a smaller limit, or page with offset", max>>20)
	}
	return body, nil
}

// login signs in with the access password and keeps the cookie.
func (s *Server) login(ctx context.Context) error {
	if s.Password == "" {
		return errUnauthorized
	}
	form := url.Values{"password": {s.Password}, "next": {"/"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Base+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("sign in: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		return fmt.Errorf("sign in: HTTP %d (is HUNTDECK_PASSWORD right?)", res.StatusCode)
	}
	if len(s.jar.Cookies(res.Request.URL)) == 0 {
		return errors.New("sign in: the instance set no session cookie")
	}
	return nil
}
