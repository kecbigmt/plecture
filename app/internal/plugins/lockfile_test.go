package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/confighome"
)

func TestDefaultLockfilePath_HonorsConfigHomeEnvVar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configHome := filepath.Join(t.TempDir(), "custom-config")
	t.Setenv(confighome.EnvVar, configHome)

	want := filepath.Join(configHome, "plect.lock")
	got, err := DefaultLockfilePath()
	if err != nil {
		t.Fatalf("DefaultLockfilePath() error = %v", err)
	}
	if got != want {
		t.Fatalf("DefaultLockfilePath() = %q, want %q", got, want)
	}
}

func TestLoadLockfile_MissingFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plect.lock")

	lf, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: unexpected error: %v", err)
	}
	if lf.SchemaVersion != LockfileSchemaVersion || len(lf.Plugins) != 0 || len(lf.Catalogs) != 0 {
		t.Fatalf("Lockfile = %+v, want empty v%d", lf, LockfileSchemaVersion)
	}
}

func TestLoadLockfile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plect.lock")
	content := `
schema_version = 2

[[catalogs]]
alias = "official"
catalog_source = "git+https://github.com/example/plect-plugins"
catalog_resolved_revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"

[[plugins]]
id = "official/github"
catalog_alias = "official"
catalog_source = "git+https://github.com/example/plect-plugins"
catalog_resolved_revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"
path = "github"
content_hash = "sha256:abc"
version = "0.3.0"
plect_min_version = "0.8.0"
editable = false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lf, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: unexpected error: %v", err)
	}
	if len(lf.Catalogs) != 1 || lf.Catalogs[0].Alias != "official" {
		t.Fatalf("Catalogs = %+v", lf.Catalogs)
	}
	if len(lf.Plugins) != 1 || lf.Plugins[0].ID != "official/github" || lf.Plugins[0].ContentHash != "sha256:abc" {
		t.Fatalf("Plugins = %+v", lf.Plugins)
	}
}

func TestLoadLockfile_UnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plect.lock")
	if err := os.WriteFile(path, []byte("schema_version = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLockfile(path); err == nil {
		t.Fatal("want error for an unknown schema_version, got nil")
	}
}

func TestLoadLockfile_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plect.lock")
	if err := os.WriteFile(path, []byte("not valid toml [[["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLockfile(path); err == nil {
		t.Fatal("want error for malformed plect.lock, got nil")
	}
}

func TestSaveLockfile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "plect.lock")
	lf := &Lockfile{
		SchemaVersion: LockfileSchemaVersion,
		Catalogs:      []CatalogLockRecord{{Alias: "official", CatalogSource: "git+https://x", CatalogResolvedRevision: "abc"}},
		Plugins:       []PluginLockEntry{{ID: "official/github", CatalogAlias: "official", Path: "github", ContentHash: "sha256:abc"}},
	}

	if err := SaveLockfile(path, lf); err != nil {
		t.Fatalf("SaveLockfile: unexpected error: %v", err)
	}

	got, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: unexpected error: %v", err)
	}
	if len(got.Catalogs) != 1 || got.Catalogs[0] != lf.Catalogs[0] {
		t.Fatalf("round-tripped Catalogs = %+v, want %+v", got.Catalogs, lf.Catalogs)
	}
	if len(got.Plugins) != 1 || got.Plugins[0] != lf.Plugins[0] {
		t.Fatalf("round-tripped Plugins = %+v, want %+v", got.Plugins, lf.Plugins)
	}
}

func TestLockfile_CatalogFindPutAndPluginFindPutRemove(t *testing.T) {
	lf := &Lockfile{}
	lf.PutCatalog(CatalogLockRecord{Alias: "official", CatalogResolvedRevision: "v1"})
	if got, ok := lf.FindCatalog("official"); !ok || got.CatalogResolvedRevision != "v1" {
		t.Fatalf("FindCatalog = %+v, %v", got, ok)
	}
	lf.PutCatalog(CatalogLockRecord{Alias: "official", CatalogResolvedRevision: "v2"})
	if got, ok := lf.FindCatalog("official"); !ok || got.CatalogResolvedRevision != "v2" {
		t.Fatalf("FindCatalog after update = %+v, %v", got, ok)
	}
	if len(lf.Catalogs) != 1 {
		t.Fatalf("Catalogs = %+v, want 1 entry (Put replaces, not appends)", lf.Catalogs)
	}

	lf.PutPlugin(PluginLockEntry{ID: "official/github", ContentHash: "sha256:v1"})
	lf.PutPlugin(PluginLockEntry{ID: "official/okf", ContentHash: "sha256:okf"})
	if got, ok := lf.FindPlugin("official/github"); !ok || got.ContentHash != "sha256:v1" {
		t.Fatalf("FindPlugin = %+v, %v", got, ok)
	}
	lf.PutPlugin(PluginLockEntry{ID: "official/github", ContentHash: "sha256:v2"})
	if got, ok := lf.FindPlugin("official/github"); !ok || got.ContentHash != "sha256:v2" {
		t.Fatalf("FindPlugin after update = %+v, %v", got, ok)
	}
	if len(lf.Plugins) != 2 {
		t.Fatalf("Plugins = %+v, want 2 entries", lf.Plugins)
	}

	lf.RemovePlugin("official/github")
	if _, ok := lf.FindPlugin("official/github"); ok {
		t.Fatal("RemovePlugin did not remove the entry")
	}
	if len(lf.Plugins) != 1 || lf.Plugins[0].ID != "official/okf" {
		t.Fatalf("Plugins after remove = %+v", lf.Plugins)
	}
}
