package plugins

import (
	"fmt"
	"path/filepath"
)

// ErrHashMismatch is returned when resolved content no longer matches the
// hash it was pinned against — tamper detection, for both git and path
// sources.
type ErrHashMismatch struct {
	Path string
	Want string
	Got  string
}

func (e *ErrHashMismatch) Error() string {
	return fmt.Sprintf("%s: content hash mismatch: declared %s, computed %s", e.Path, e.Want, e.Got)
}

// resolvePathTree resolves a path:// source's symlinks and hashes its tree.
// It performs no verification against any pinned hash: Fetch uses it to
// compute the hash to pin in the first place, and VerifyAndMount compares
// its result against the lockfile itself.
func resolvePathTree(rawPath string) (dir, contentHash string, err error) {
	resolved, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve path source %s: %w", rawPath, err)
	}
	hash, err := HashTree(resolved)
	if err != nil {
		return "", "", fmt.Errorf("hash path source %s: %w", resolved, err)
	}
	return resolved, hash, nil
}
