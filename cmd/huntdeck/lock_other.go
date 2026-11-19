//go:build !unix

package main

import "errors"

// lockData is a no-op where flock does not exist: the check it enables is a
// convenience for the operator, not a safety property of the data.
func lockData(dir string) (func(), error) {
	return nil, errors.New("data directory locking is not available on this platform; make sure no HuntDeck is running")
}
