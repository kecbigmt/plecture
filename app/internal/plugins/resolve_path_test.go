package plugins

import (
	"path/filepath"
	"testing"
)

func TestResolvePathTree(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"plugin.toml": "name = \"okf\"\n"})

	resolved, hash, err := resolvePathTree(dir)
	if err != nil {
		t.Fatalf("resolvePathTree: unexpected error: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
	wantHash, err := HashTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hash != wantHash {
		t.Errorf("hash = %q, want %q", hash, wantHash)
	}
}

func TestResolvePathTree_MissingPath(t *testing.T) {
	if _, _, err := resolvePathTree(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error for a nonexistent path source, got nil")
	}
}
