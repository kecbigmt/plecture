package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCatalogManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalPlugin(t *testing.T, dir string) {
	t.Helper()
	writeManifest(t, dir, "schema_version = 1\nplect_min_version = \"0.1.0\"\n")
}

func TestLoadCatalogManifest_Valid(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `
schema_version = 1
description = "Example catalog."
plugins = ["github", "agent/runtime"]
`)
	writeMinimalPlugin(t, filepath.Join(root, "github"))
	writeMinimalPlugin(t, filepath.Join(root, "agent", "runtime"))

	m, err := LoadCatalogManifest(root)
	if err != nil {
		t.Fatalf("LoadCatalogManifest: unexpected error: %v", err)
	}
	if len(m.Plugins) != 2 || m.Description != "Example catalog." {
		t.Fatalf("Manifest = %+v", m)
	}
}

func TestLoadCatalogManifest_MissingFile(t *testing.T) {
	if _, err := LoadCatalogManifest(t.TempDir()); err == nil {
		t.Fatal("want error for missing catalog.toml, got nil")
	}
}

func TestLoadCatalogManifest_UnknownSchemaVersion(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `
schema_version = 2
plugins = ["x"]
`)
	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for an unknown schema_version, got nil")
	}
}

func TestLoadCatalogManifest_EmptyPluginsList(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `schema_version = 1`)
	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for an empty plugins list, got nil")
	}
}

func TestLoadCatalogManifest_ListedPathEscapesRoot(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `
schema_version = 1
plugins = ["../escape"]
`)
	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for a plugin path outside the catalog root, got nil")
	}
}

func TestLoadCatalogManifest_ListedPathMissingPluginToml(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `
schema_version = 1
plugins = ["github"]
`)
	if err := os.MkdirAll(filepath.Join(root, "github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for a listed path with no plugin.toml, got nil")
	}
}

func TestLoadCatalogManifest_UnlistedPluginTomlFailsLoud(t *testing.T) {
	root := t.TempDir()
	writeCatalogManifest(t, root, `
schema_version = 1
plugins = ["github"]
`)
	writeMinimalPlugin(t, filepath.Join(root, "github"))
	writeMinimalPlugin(t, filepath.Join(root, "extra")) // present on disk, not listed

	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for an unlisted plugin.toml under the catalog root, got nil")
	}
}

func TestResolveCatalogSubdir_Empty(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveCatalogSubdir(root, "")
	if err != nil {
		t.Fatalf("ResolveCatalogSubdir: unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("ResolveCatalogSubdir(root, \"\") = %q, want %q", got, root)
	}
}

func TestResolveCatalogSubdir_Subdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCatalogSubdir(root, "plugins")
	if err != nil {
		t.Fatalf("ResolveCatalogSubdir: unexpected error: %v", err)
	}
	want := filepath.Join(root, "plugins")
	if got != want {
		t.Errorf("ResolveCatalogSubdir(root, \"plugins\") = %q, want %q", got, want)
	}
}

// TestResolveCatalogSubdir_MultiSegmentSubdirectory is the GWT the issue
// asked for directly: subdir accepts a relative subpath of any depth, not
// just a single directory name, and the resulting catalog root joins the
// full multi-segment path.
func TestResolveCatalogSubdir_MultiSegmentSubdirectory(t *testing.T) {
	root := t.TempDir()
	multiSegment := filepath.Join("path", "to", "plugins")
	if err := os.MkdirAll(filepath.Join(root, multiSegment), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCatalogSubdir(root, multiSegment)
	if err != nil {
		t.Fatalf("ResolveCatalogSubdir: unexpected error: %v", err)
	}
	want := filepath.Join(root, multiSegment)
	if got != want {
		t.Errorf("ResolveCatalogSubdir(root, %q) = %q, want %q", multiSegment, got, want)
	}
}

func TestResolveCatalogSubdir_ParentEscapeRejected(t *testing.T) {
	root := t.TempDir()
	for _, subdir := range []string{"..", "../escape", "/etc"} {
		if _, err := ResolveCatalogSubdir(root, subdir); err == nil {
			t.Errorf("ResolveCatalogSubdir(root, %q): want error, got nil", subdir)
		}
	}
}

func TestResolveCatalogSubdir_SymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	if _, err := ResolveCatalogSubdir(root, "linked"); err == nil {
		t.Fatal("want error for a subdir that symlinks outside the source root, got nil")
	}
}

func TestLoadCatalogManifest_SymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeMinimalPlugin(t, outside)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	writeCatalogManifest(t, root, `
schema_version = 1
plugins = ["linked"]
`)

	if _, err := LoadCatalogManifest(root); err == nil {
		t.Fatal("want error for a plugin path that symlinks outside the catalog root, got nil")
	}
}
