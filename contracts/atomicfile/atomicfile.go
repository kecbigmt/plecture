// Package atomicfile provides one tmp-write-fsync-rename primitive for the
// durable on-disk paths (state, subscription registries, event cursors) that
// must survive a crash without corruption. It replaces several near-identical
// hand-rolled versions that had drifted apart (some skipped fsync, only the
// event cursor had it).
//
// This module is zero-dependency (stdlib only) so app and plugins can all
// import it, mirroring contracts/{state,hook,channel-protocol,event}.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data: write to a sibling temp file,
// fsync its contents, close, then rename over path. The parent directory
// must already exist (MkdirAll is caller policy: mode/ownership vary by call
// site). Rename is atomic on a POSIX filesystem, so a crash before it leaves
// path untouched, and a crash after it leaves at most an orphaned temp file
// for the next write to overwrite.
//
// Write does not fsync the parent directory after rename. That would
// additionally guarantee the rename survives a bare power-loss crash, not
// just a process crash — a stronger bound none of sennit's current durable
// paths need, since losing the last write immediately before power loss
// degrades to "reload/resubscribe from the last durable point", not
// corruption. Add a directory fsync at the call site if a future path needs
// that stronger guarantee.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".atomicfile-*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("atomicfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("atomicfile: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}
