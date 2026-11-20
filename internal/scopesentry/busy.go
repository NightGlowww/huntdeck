package scopesentry

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBusy is the scanner saying "not now": its node queue is full, or the
// instance is creating tasks faster than its own limit allows. Neither is a
// fault in HuntDeck or in the scanner - the scheduler simply has to wait.
//
// Observed answers, verbatim from the instance:
//
//	{"error": "节点 node-sh-01 队列已积压 5 个任务（上限 5），请等待当前任务完成后再提交"}
//	400 api.task.rate_limited 「每分钟最多创建 10 个任务，当前已创建 10 个」
var ErrBusy = errors.New("the scanner is busy")

// busyMarkers are what the scanner says when it is refusing work for its own
// capacity reasons rather than because the request was wrong.
var busyMarkers = []string{
	"队列已积压",
	"rate_limited",
	"每分钟最多创建",
	"请等待当前任务完成",
}

// IsBusy reports whether err is the scanner's backpressure rather than a fault.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBusy) {
		return true
	}
	msg := err.Error()
	for _, m := range busyMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// busy wraps the scanner's own words so the caller can tell capacity from
// breakage while the operator still sees what the scanner said.
func busy(err error) error {
	if err == nil || !IsBusy(err) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrBusy, err)
}
