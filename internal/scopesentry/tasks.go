package scopesentry

import (
	"context"
	"encoding/json"
	"fmt"
)

// Template is one scan template on the ScopeSentry side.
type Template struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Node is one scan node. ScopeSentry reports everything as strings, and
// "state" is its online flag (1 online, 0 offline).
type Node struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Version string `json:"version"`
	Running string `json:"running"`
}

// Online reports whether the node is up.
func (n Node) Online() bool { return n.State == "1" }

// Templates lists the scan templates a task can name.
func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	raw, err := c.Call(ctx, "list_scan_templates", map[string]any{"pageIndex": 1, "pageSize": 100})
	if err != nil {
		return nil, err
	}
	return decodeList[Template](raw, "list", "data")
}

// Nodes lists the scan nodes.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	raw, err := c.Call(ctx, "list_nodes", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeList[Node](raw, "list", "nodes", "data")
}

// CreateTask creates a scan task for the given targets and returns its id.
// template is a template id from Templates; node is a node name from Nodes.
func (c *Client) CreateTask(ctx context.Context, name, template, node string, targets []string) (string, error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("scopesentry: no targets")
	}
	args := map[string]any{
		"name":         name,
		"node":         []string{node},
		"template":     template,
		"target":       joinTargets(targets),
		"targetSource": "general",
	}
	raw, err := c.Call(ctx, "create_scan_task", args)
	if err != nil {
		return "", busy(err)
	}
	var out struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", fmt.Errorf("scopesentry: create_scan_task: %w", err)
	}
	if out.ID != "" {
		return out.ID, nil
	}
	if out.Data.ID != "" {
		return out.Data.ID, nil
	}
	return "", fmt.Errorf("scopesentry: create_scan_task answered %s", truncate(raw, 200))
}

// StartTask queues a created task.
func (c *Client) StartTask(ctx context.Context, id string) error {
	_, err := c.Call(ctx, "start_task", map[string]any{"id": id})
	return err
}

// joinTargets writes targets the way the target field expects: one per line.
func joinTargets(targets []string) string {
	out := ""
	for i, t := range targets {
		if i > 0 {
			out += "\n"
		}
		out += t
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// decodeList reads a list answer in whichever wrapper the tool used.
func decodeList[T any](raw string, keys ...string) ([]T, error) {
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, err
	}
	for _, k := range keys {
		v, ok := probe[k]
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var out []T
		if err := json.Unmarshal(b, &out); err == nil && len(out) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("scopesentry: no list in %s", truncate(raw, 200))
}
