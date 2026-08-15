package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func catalogTestPaths(t *testing.T) PluginPaths {
	t.Helper()
	dir := t.TempDir()
	return PluginPaths{
		CatalogsPath: filepath.Join(dir, "catalogs.toml"),
		LockfilePath: filepath.Join(dir, "plect.lock"),
		CacheRoot:    filepath.Join(dir, "cache"),
	}
}

func writeCatalogSource(t *testing.T, plugins map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name := range plugins {
		names = append(names, name)
	}
	manifest := "schema_version = 1\nplugins = ["
	for i, name := range names {
		if i > 0 {
			manifest += ", "
		}
		manifest += "\"" + name + "\""
	}
	manifest += "]\n"
	if err := os.WriteFile(filepath.Join(dir, "catalog.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, minVersion := range plugins {
		pluginDir := filepath.Join(dir, name)
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "schema_version = 1\nplect_min_version = \"" + minVersion + "\"\n"
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPreviewCatalogAdd_RequiresAliasAndSource(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, _, err := PreviewCatalogAdd(context.Background(), paths, CatalogAddParams{}); err == nil {
		t.Fatal("want error for empty alias/source, got nil")
	}
}

func TestPreviewCatalogAdd_ResolvesCatalog(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})

	preview, fetched, err := PreviewCatalogAdd(context.Background(), paths, CatalogAddParams{Alias: "local", Source: "path+editable://" + src})
	if err != nil {
		t.Fatalf("PreviewCatalogAdd: unexpected error: %v", err)
	}
	if preview.Alias != "local" || len(preview.Plugins) != 1 || preview.Plugins[0] != "okf" {
		t.Fatalf("preview = %+v", preview)
	}
	if fetched.Manifest.Plugins[0] != "okf" {
		t.Fatalf("fetched = %+v", fetched)
	}
}

func TestCommitCatalogAdd_PersistsRegistrationAndLock(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	params := CatalogAddParams{Alias: "local", Source: "path+editable://" + src}

	_, fetched, err := PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CommitCatalogAdd(paths, params, fetched)
	if err != nil {
		t.Fatalf("CommitCatalogAdd: unexpected error: %v", err)
	}
	if got.Alias != "local" {
		t.Errorf("Alias = %q", got.Alias)
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(registrations.Catalogs) != 1 || len(registrations.Catalogs[0].Plugins) != 0 {
		t.Fatalf("Catalogs = %+v, want one entry with an empty plugins list", registrations.Catalogs)
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.FindCatalog("local"); !ok {
		t.Fatal("catalog lock record not written")
	}
}

func TestCommitCatalogAdd_AlreadyRegisteredFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	params := CatalogAddParams{Alias: "local", Source: "path+editable://" + src}

	_, fetched, err := PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitCatalogAdd(paths, params, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitCatalogAdd(paths, params, fetched); err == nil {
		t.Fatal("want error for an already-registered alias, got nil")
	}
}

func addTestCatalog(t *testing.T, paths PluginPaths, alias, source string) {
	t.Helper()
	params := CatalogAddParams{Alias: alias, Source: source}
	_, fetched, err := PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitCatalogAdd(paths, params, fetched); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogUpdate_UnregisteredAliasFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, err := CatalogUpdate(context.Background(), paths, CatalogUpdateParams{Alias: "missing"}); err == nil {
		t.Fatal("want error for an unregistered alias, got nil")
	}
}

func TestCatalogUpdate_PathSourceRejectsRevisionFlag(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)

	if _, err := CatalogUpdate(context.Background(), paths, CatalogUpdateParams{Alias: "local", Revision: "v1"}); err == nil {
		t.Fatal("want error when --revision is passed for a path-sourced catalog, got nil")
	}
}

func TestCatalogUpdate_RepointsEnabledPlugins(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}
	before, ok := (func() (plugins.PluginLockEntry, bool) {
		lf, err := plugins.LoadLockfile(paths.LockfilePath)
		if err != nil {
			t.Fatal(err)
		}
		return lf.FindPlugin("local/okf")
	})()
	if !ok {
		t.Fatal("expected local/okf to be locked after PluginAdd")
	}

	// Change source content, then update the catalog.
	if err := os.WriteFile(filepath.Join(src, "okf", "plugin.toml"), []byte("schema_version = 1\nplect_min_version = \"0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := CatalogUpdate(context.Background(), paths, CatalogUpdateParams{Alias: "local"})
	if err != nil {
		t.Fatalf("CatalogUpdate: unexpected error: %v", err)
	}
	if len(result.UpdatedPlugins) != 1 || result.UpdatedPlugins[0] != "okf" {
		t.Fatalf("UpdatedPlugins = %v", result.UpdatedPlugins)
	}

	lf, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	after, ok := lf.FindPlugin("local/okf")
	if !ok {
		t.Fatal("local/okf lock entry disappeared after update")
	}
	if after.ContentHash == before.ContentHash {
		t.Fatal("CatalogUpdate did not re-hash the changed plugin content")
	}
}

func TestPreviewCatalogRemove_ReportsDisabledPlugins(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	preview, err := PreviewCatalogRemove(paths, "local")
	if err != nil {
		t.Fatalf("PreviewCatalogRemove: unexpected error: %v", err)
	}
	if len(preview.DisabledPlugins) != 1 || preview.DisabledPlugins[0] != "okf" {
		t.Fatalf("DisabledPlugins = %v", preview.DisabledPlugins)
	}
}

func TestCommitCatalogRemove_RemovesRegistrationAndLocks(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	if _, err := CommitCatalogRemove(paths, "local"); err != nil {
		t.Fatalf("CommitCatalogRemove: unexpected error: %v", err)
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(registrations.Catalogs) != 0 {
		t.Fatalf("Catalogs = %+v, want empty", registrations.Catalogs)
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Catalogs) != 0 || len(lock.Plugins) != 0 {
		t.Fatalf("Lockfile = %+v, want empty", lock)
	}
}

func TestCommitCatalogRemove_UnregisteredAliasFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, err := CommitCatalogRemove(paths, "missing"); err == nil {
		t.Fatal("want error for an unregistered alias, got nil")
	}
}

func TestCatalogList_ReportsRegisteredCatalogs(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path+editable://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	got, err := CatalogList(paths)
	if err != nil {
		t.Fatalf("CatalogList: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Alias != "local" || got[0].Status != "ok" || len(got[0].EnabledPlugins) != 1 {
		t.Fatalf("CatalogList = %+v", got)
	}
}
