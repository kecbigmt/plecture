package plugins

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultCacheRoot returns ~/.cache/plect/catalogs, the root resolved
// catalog snapshots are materialized under.
func DefaultCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "plect", "catalogs"), nil
}

// SourceDigest is the content-addressing key for a catalog's cache
// namespace: derived from the exact registered source, not the user-local
// alias, so reusing an alias for a different catalog can never reuse the
// old catalog's cache.
func SourceDigest(source string) string {
	sum := sha256.Sum256([]byte(source))
	return fmt.Sprintf("%x", sum)
}

// CacheDir is the mount point for a resolved catalog snapshot:
// cacheRoot/<source-digest>/<lock-coordinate>/, where lock-coordinate is
// the resolved commit SHA for a git catalog. Locked and editable path
// catalogs are never cached — they mount their source path directly.
func CacheDir(cacheRoot, source, lockCoordinate string) string {
	return filepath.Join(cacheRoot, SourceDigest(source), lockCoordinate)
}
