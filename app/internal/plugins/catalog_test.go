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

func TestResolveCatalogDir_Empty(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveCatalogDir(root, "")
	if err != nil {
		t.Fatalf("ResolveCatalogDir: unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("ResolveCatalogDir(root, \"\") = %q, want %q", got, root)
	}
}

func TestResolveCatalogDir_Subdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCatalogDir(root, "plugins")
	if err != nil {
		t.Fatalf("ResolveCatalogDir: unexpected error: %v", err)
	}
	want := filepath.Join(root, "plugins")
	if got != want {
		t.Errorf("ResolveCatalogDir(root, \"plugins\") = %q, want %q", got, want)
	}
}

func TestResolveCatalogDir_ParentEscapeRejected(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"..", "../escape", "/etc"} {
		if _, err := ResolveCatalogDir(root, dir); err == nil {
			t.Errorf("ResolveCatalogDir(root, %q): want error, got nil", dir)
		}
	}
}

func TestResolveCatalogDir_SymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	if _, err := ResolveCatalogDir(root, "linked"); err == nil {
		t.Fatal("want error for a dir that symlinks outside the source root, got nil")
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
