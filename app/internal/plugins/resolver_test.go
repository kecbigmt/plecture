package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

const testPlectVersion = "0.8.0"

func TestVerifyAndMountCatalog_GitSource_MissingLockFailsLoud(t *testing.T) {
	repo, _, _ := newLocalCatalogGitRepo(t)
	entry := CatalogEntry{Alias: "official", Source: "git+https://" + repo, Plugins: []string{"okf"}}

	_, err := VerifyAndMountCatalog(entry, &Lockfile{}, t.TempDir())
	var want *ErrMissingCatalogLock
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrMissingCatalogLock", err)
	}
}

func TestVerifyAndMountCatalog_GitSource_SourceDriftFailsLoud(t *testing.T) {
	repo, firstCommit, _ := newLocalCatalogGitRepo(t)
	entry := CatalogEntry{Alias: "official", Source: "git+https://" + repo, Plugins: []string{"okf"}}
	lock := &Lockfile{Catalogs: []CatalogLockRecord{{
		Alias: "official", CatalogSource: "git+https://different-source", CatalogResolvedRevision: firstCommit,
	}}}

	_, err := VerifyAndMountCatalog(entry, lock, t.TempDir())
	var want *ErrCatalogSourceDrift
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrCatalogSourceDrift", err)
	}
}

func TestVerifyAndMountCatalog_GitSource_Success(t *testing.T) {
	repo, firstCommit, _ := newLocalCatalogGitRepo(t)
	cacheRoot := t.TempDir()
	source := "git+https://" + repo
	if _, _, err := fetchGitCatalog(context.Background(), procexec.Default, source, repo, firstCommit, cacheRoot); err != nil {
		t.Fatal(err)
	}

	entry := CatalogEntry{Alias: "official", Source: source, Plugins: []string{"okf"}}
	lock := &Lockfile{Catalogs: []CatalogLockRecord{{Alias: "official", CatalogSource: source, CatalogResolvedRevision: firstCommit}}}

	rc, err := VerifyAndMountCatalog(entry, lock, cacheRoot)
	if err != nil {
		t.Fatalf("VerifyAndMountCatalog: unexpected error: %v", err)
	}
	if rc.NonReproducible {
		t.Error("NonReproducible = true, want false for a locked git catalog")
	}
	if len(rc.Manifest.Plugins) != 1 || rc.Manifest.Plugins[0] != "okf" {
		t.Errorf("Manifest.Plugins = %v", rc.Manifest.Plugins)
	}
}

func TestVerifyAndMountCatalog_PathEditable_NeedsNoLock(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	writeMinimalPlugin(t, filepath.Join(dir, "okf"))

	entry := CatalogEntry{Alias: "local", Source: "path+editable://" + dir, Plugins: []string{"okf"}}

	rc, err := VerifyAndMountCatalog(entry, &Lockfile{}, t.TempDir())
	if err != nil {
		t.Fatalf("VerifyAndMountCatalog: unexpected error: %v", err)
	}
	if !rc.NonReproducible {
		t.Error("NonReproducible = false, want true for an editable path catalog")
	}
}

func TestVerifyAndMountCatalog_PathLocked_RequiresCatalogLock(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	writeMinimalPlugin(t, filepath.Join(dir, "okf"))

	entry := CatalogEntry{Alias: "local", Source: "path://" + dir, Plugins: []string{"okf"}}

	_, err := VerifyAndMountCatalog(entry, &Lockfile{}, t.TempDir())
	var want *ErrMissingCatalogLock
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrMissingCatalogLock", err)
	}
}

func TestVerifyAndMountPlugin_MissingLockFailsLoud(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	writeMinimalPlugin(t, filepath.Join(dir, "okf"))
	catalog := ResolvedCatalog{Alias: "local", Root: dir, Manifest: CatalogManifest{Plugins: []string{"okf"}}}

	_, err := VerifyAndMountPlugin(catalog, "okf", &Lockfile{}, testPlectVersion)
	var want *ErrMissingPluginLock
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrMissingPluginLock", err)
	}
}

func TestVerifyAndMountPlugin_TamperedContentFailsLoud(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	pluginDir := filepath.Join(dir, "okf")
	writeMinimalPlugin(t, pluginDir)
	hash, err := HashTree(pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	catalog := ResolvedCatalog{Alias: "local", Root: dir, Manifest: CatalogManifest{Plugins: []string{"okf"}}}
	lock := &Lockfile{Plugins: []PluginLockEntry{{ID: "local/okf", Path: "okf", ContentHash: hash}}}

	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte("schema_version = 1\nplect_min_version = \"9.9.9\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = VerifyAndMountPlugin(catalog, "okf", lock, testPlectVersion)
	var want *ErrHashMismatch
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrHashMismatch", err)
	}
}

func TestVerifyAndMountPlugin_LockedSuccess(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	pluginDir := filepath.Join(dir, "okf")
	writeMinimalPlugin(t, pluginDir)
	hash, err := HashTree(pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	catalog := ResolvedCatalog{Alias: "local", Root: dir, Manifest: CatalogManifest{Plugins: []string{"okf"}}}
	lock := &Lockfile{Plugins: []PluginLockEntry{{ID: "local/okf", Path: "okf", ContentHash: hash}}}

	mounted, err := VerifyAndMountPlugin(catalog, "okf", lock, testPlectVersion)
	if err != nil {
		t.Fatalf("VerifyAndMountPlugin: unexpected error: %v", err)
	}
	if mounted.ID != "local/okf" || mounted.NonReproducible {
		t.Errorf("Mounted = %+v", mounted)
	}
}

func TestVerifyAndMountPlugin_EditableCatalogNeedsNoLockEntry(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "okf")
	writeMinimalPlugin(t, pluginDir)
	catalog := ResolvedCatalog{Alias: "local", Root: dir, NonReproducible: true, Manifest: CatalogManifest{Plugins: []string{"okf"}}}

	mounted, err := VerifyAndMountPlugin(catalog, "okf", &Lockfile{}, testPlectVersion)
	if err != nil {
		t.Fatalf("VerifyAndMountPlugin: unexpected error: %v", err)
	}
	if !mounted.NonReproducible {
		t.Error("NonReproducible = false, want true when the owning catalog is editable")
	}
}

func TestVerifyAndMountPlugin_IncompatibleMinVersionFailsLoud(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "okf")
	writeManifest(t, pluginDir, "schema_version = 1\nplect_min_version = \"99.0.0\"\n")
	catalog := ResolvedCatalog{Alias: "local", Root: dir, NonReproducible: true, Manifest: CatalogManifest{Plugins: []string{"okf"}}}

	_, err := VerifyAndMountPlugin(catalog, "okf", &Lockfile{}, testPlectVersion)
	var want *ErrIncompatible
	if !errors.As(err, &want) {
		t.Fatalf("err = %v, want *ErrIncompatible", err)
	}
}

func TestVerifyAndMountAll_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\", \"github\"]\n")
	writeMinimalPlugin(t, filepath.Join(dir, "okf"))
	writeMinimalPlugin(t, filepath.Join(dir, "github"))
	okfHash, err := HashTree(filepath.Join(dir, "okf"))
	if err != nil {
		t.Fatal(err)
	}
	githubHash, err := HashTree(filepath.Join(dir, "github"))
	if err != nil {
		t.Fatal(err)
	}

	registrations := &CatalogRegistrations{Catalogs: []CatalogEntry{
		{Alias: "local", Source: "path://" + dir, Plugins: []string{"okf", "github"}},
	}}
	lock := &Lockfile{
		Catalogs: []CatalogLockRecord{{Alias: "local", CatalogSource: "path://" + dir}},
		Plugins: []PluginLockEntry{
			{ID: "local/okf", Path: "okf", ContentHash: okfHash},
			{ID: "local/github", Path: "github", ContentHash: githubHash},
		},
	}

	mounted, err := VerifyAndMountAll(registrations, lock, t.TempDir(), testPlectVersion)
	if err != nil {
		t.Fatalf("VerifyAndMountAll: unexpected error: %v", err)
	}
	if len(mounted) != 2 {
		t.Fatalf("mounted = %+v, want 2 entries", mounted)
	}
	ids := map[string]bool{mounted[0].ID: true, mounted[1].ID: true}
	if !ids["local/okf"] || !ids["local/github"] {
		t.Errorf("ids = %v", ids)
	}
}

func TestVerifyAndMountAll_EnabledPathNotListedInCatalogFailsLoud(t *testing.T) {
	dir := t.TempDir()
	writeCatalogManifest(t, dir, "schema_version = 1\nplugins = [\"okf\"]\n")
	writeMinimalPlugin(t, filepath.Join(dir, "okf"))

	// "github" is enabled in the user's registration but not published by
	// the catalog's own manifest.
	registrations := &CatalogRegistrations{Catalogs: []CatalogEntry{
		{Alias: "local", Source: "path://" + dir, Plugins: []string{"okf", "github"}},
	}}
	lock := &Lockfile{Catalogs: []CatalogLockRecord{{Alias: "local", CatalogSource: "path://" + dir}}}

	if _, err := VerifyAndMountAll(registrations, lock, t.TempDir(), testPlectVersion); err == nil {
		t.Fatal("want error when an enabled plugin path is not listed by the catalog, got nil")
	}
}
