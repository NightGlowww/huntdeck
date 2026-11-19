//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// lockData takes an exclusive lock on <dir>/huntdeck.lock for the life of the
// process: it is how a restore knows a server is running against this data
// directory. The kernel drops it when the process ends, crash included.
func lockData(dir string) (func(), error) {
	path := dir + "/huntdeck.lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another HuntDeck holds %s: stop it first (%w)", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
