//go:build !unix

package main

import "errors"

// diskFree is unavailable here; see diskfree_unix.go.
func diskFree(string) (int64, int64, error) {
	return 0, 0, errors.New("disk space unknown on this system")
}
