package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHashTree_DeterministicForIdenticalContent(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	files := map[string]string{
		"plugin.toml":       "name = \"x\"\n",
		"providers/x.toml":  "setup = \"true\"\n",
		"bin/plect-x-watch": "#!/bin/sh\necho hi\n",
	}
	writeTree(t, a, files)
	writeTree(t, b, files)

	ha, err := HashTree(a)
	if err != nil {
		t.Fatalf("HashTree(a): unexpected error: %v", err)
	}
	hb, err := HashTree(b)
	if err != nil {
		t.Fatalf("HashTree(b): unexpected error: %v", err)
	}
	if ha != hb {
		t.Fatalf("HashTree differs for identical trees: %q vs %q", ha, hb)
	}
}

func TestHashTree_DiffersOnContentChange(t *testing.T) {
	a := t.TempDir()
	writeTree(t, a, map[string]string{"plugin.toml": "name = \"x\"\n"})
	h1, err := HashTree(a)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(a, "plugin.toml"), []byte("name = \"y\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := HashTree(a)
	if err != nil {
		t.Fatal(err)
	}

	if h1 == h2 {
		t.Fatal("HashTree did not change after tampering with a tracked file")
	}
}

func TestHashTree_DiffersOnRenameWithSameBytes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeTree(t, a, map[string]string{"providers/x.toml": "setup = \"true\"\n"})
	writeTree(t, b, map[string]string{"providers/y.toml": "setup = \"true\"\n"})

	ha, err := HashTree(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := HashTree(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatal("HashTree must key by path, not just by byte content")
	}
}

func TestHashTree_ExcludesDotGit(t *testing.T) {
	a := t.TempDir()
	writeTree(t, a, map[string]string{"plugin.toml": "name = \"x\"\n"})
	h1, err := HashTree(a)
	if err != nil {
		t.Fatal(err)
	}

	// A cloned git source carries a .git directory that a path source never
	// has; it must not affect the hash or identical content would hash
	// differently depending on which resolution scheme produced it.
	writeTree(t, a, map[string]string{".git/HEAD": "ref: refs/heads/main\n"})
	h2, err := HashTree(a)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("HashTree must exclude .git")
	}
}

func TestHashTreeExcluding_OmitsExcludedPath(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"plugin.toml": "name = \"x\"\n",
		"bin/built":   "v1",
	})
	h1, err := HashTreeExcluding(dir, []string{"bin/built"})
	if err != nil {
		t.Fatal(err)
	}

	// Changing the excluded file's content must not move the hash.
	if err := os.WriteFile(filepath.Join(dir, "bin", "built"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := HashTreeExcluding(dir, []string{"bin/built"})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("HashTreeExcluding changed despite the excluded file being the only edit: %q vs %q", h1, h2)
	}

	// A non-excluded change must still move the hash.
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte("name = \"y\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, err := HashTreeExcluding(dir, []string{"bin/built"})
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h2 {
		t.Fatal("HashTreeExcluding did not change after editing a non-excluded tracked file")
	}
}

func TestHashTree_MissingDir(t *testing.T) {
	if _, err := HashTree(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("want error for missing directory, got nil")
	}
}
