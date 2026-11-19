//go:build unix

package main

import "syscall"

// diskFree is the free and total bytes of the filesystem holding path.
func diskFree(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	//nolint:gosec // byte counts from statfs: they fit in int64 for any real filesystem
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
