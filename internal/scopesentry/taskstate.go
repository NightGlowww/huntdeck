package scopesentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnknownTask is the scanner saying it has no such task: deleted there, or
// never created. It is not a failure of the pass.
var ErrUnknownTask = errors.New("scopesentry: no such task")

// TaskState is what the scanner says about one task: its status number, how
// far it got, and when it ended (if it did). get_task has carried this all
// along - get_task_progress, the tool the handoff used to read, does not.
type TaskState struct {
	ID       string
	Name     string
	Status   int
	Progress int
	EndedAt  *time.Time
}

// State reads one task's state.
func (c *Client) State(ctx context.Context, id string) (TaskState, error) {
	raw, err := c.Call(ctx, "get_task", map[string]any{"id": id})
	if err != nil {
		return TaskState{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return TaskState{}, fmt.Errorf("scopesentry: get_task %s answered nothing", id)
	}
	var body struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Status   int    `json:"status"`
		Progress int    `json:"progress"`
		EndTime  string `json:"endTime"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return TaskState{}, fmt.Errorf("scopesentry: get_task %s: %w", id, err)
	}
	if body.ID == "" {
		// The tool answered, but not with a task: deleted on the scanner's side,
		// or an error result the transport passed through as text.
		return TaskState{}, ErrUnknownTask
	}
	st := TaskState{ID: body.ID, Name: body.Name, Status: body.Status, Progress: body.Progress}
	if body.EndTime != "" {
		if at, err := parseTaskTime(body.EndTime); err == nil {
			st.EndedAt = &at
		}
	}
	return st, nil
}

// parseTaskTime reads the scanner's own timestamp format.
func parseTaskTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if at, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", s)
}

// StateReading maps the scanner's status number to the word HuntDeck keeps.
// ScopeSentry counts 0 pending, 1 running, 2 paused, 3 finished, 4 failed;
// anything else is unknown rather than a guess.
func StateReading(status int) string {
	switch status {
	case 0:
		return "queued"
	case 1:
		return "running"
	case 2:
		return "paused"
	case 3:
		return "done"
	case 4:
		return "failed"
	}
	return "unknown"
}
