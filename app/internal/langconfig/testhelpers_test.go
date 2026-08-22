package langconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up from the working directory to the one holding go.work,
// so tests can read docs/language/ and testdata/config-language/ by their
// repository-relative paths regardless of which package directory `go test`
// runs them from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found in any parent of the working directory")
		}
		dir = parent
	}
}
