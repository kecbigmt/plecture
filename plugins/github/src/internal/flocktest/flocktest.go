// Package flocktest is a test-only helper for asserting that a file
// descriptor a package opened for flock(2) was opened writable. The Linux
// NFS client rejects LOCK_EX on an O_RDONLY descriptor with EBADF, even
// though local filesystems tolerate it, so a package holding an exclusive
// lock must open its lock file O_RDWR (or O_WRONLY) — this package lets a
// test catch a regression to O_RDONLY even on a local (non-NFS) test
// filesystem.
package flocktest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// AccessMode reports the access mode (os.O_RDONLY, os.O_WRONLY, or
// os.O_RDWR) of the calling process's currently-open file descriptor for
// path, read from /proc/self/fdinfo. There is no portable way to query an
// fd's open flags from outside the process that opened it, so the caller
// must invoke this while the fd under test is still open — typically from
// inside the locked callback.
func AccessMode(path string) (int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, fmt.Errorf("read /proc/self/fd: %w", err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil || target != path {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", entry.Name()))
		if err != nil {
			return 0, fmt.Errorf("read fdinfo for fd %s: %w", entry.Name(), err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			const prefix = "flags:"
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			flags, err := strconv.ParseInt(strings.TrimSpace(line[len(prefix):]), 8, 64)
			if err != nil {
				return 0, fmt.Errorf("parse fdinfo flags %q: %w", line, err)
			}
			return int(flags) & syscall.O_ACCMODE, nil
		}
		return 0, fmt.Errorf("fdinfo for fd %s has no flags line", entry.Name())
	}
	return 0, fmt.Errorf("no open fd found for %s", path)
}
