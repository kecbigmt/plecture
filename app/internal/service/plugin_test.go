package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// handEditCatalogSubdir simulates a human directly editing catalogs.toml's
// subdir field for alias, bypassing `plect catalog add`/`remove` — the
// drift PluginAdd/PluginUpdate/CatalogUpdate must all reject before
// trusting it.
func handEditCatalogSubdir(t *testing.T, paths PluginPaths, alias, subdir string) {
	t.Helper()
	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range registrations.Catalogs {
		if registrations.Catalogs[i].Alias == alias {
			registrations.Catalogs[i].Subdir = subdir
			found = true
		}
	}
	if !found {
		t.Fatalf("handEditCatalogSubdir: alias %q not registered", alias)
	}
	if err := plugins.SaveCatalogRegistrations(paths.CatalogsPath, registrations); err != nil {
		t.Fatal(err)
	}
}

func TestPluginAdd_UnregisteredCatalogFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err == nil {
		t.Fatal("want error when the catalog alias is not registered, got nil")
	}
}

func TestPluginAdd_MalformedIDFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, err := PluginAdd(context.Background(), paths, "noslash"); err == nil {
		t.Fatal("want error for a malformed plugin id, got nil")
	}
}

func TestPluginAdd_UnpublishedPathFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)

	if _, err := PluginAdd(context.Background(), paths, "local/not-published"); err == nil {
		t.Fatal("want error for a path the catalog does not publish, got nil")
	}
}

func TestPluginAdd_PersistsSelectionAndLock(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)

	got, err := PluginAdd(context.Background(), paths, "local/okf")
	if err != nil {
		t.Fatalf("PluginAdd: unexpected error: %v", err)
	}
	if got.ID != "local/okf" {
		t.Errorf("ID = %q", got.ID)
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := registrations.Find("local")
	if len(entry.Plugins) != 1 || entry.Plugins[0] != "okf" {
		t.Fatalf("enabled plugins = %v", entry.Plugins)
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.FindPlugin("local/okf"); !ok {
		t.Fatal("plugin lock entry not written")
	}
}

func TestPluginAdd_AlreadyEnabledFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)

	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err == nil {
		t.Fatal("want error for an already-enabled plugin, got nil")
	}
}

// TestPluginAddThenVerifyAndMountAll_ResolvesLocked exercises add → the
// ordinary, non-editable load path end-to-end: after add, VerifyAndMountAll
// must succeed purely from local state (no re-fetch).
func TestPluginAddThenVerifyAndMountAll_ResolvesLocked(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	mounted, err := plugins.VerifyAndMountAll(registrations, lock, paths.CacheRoot, "0.0.0")
	if err != nil {
		t.Fatalf("VerifyAndMountAll: unexpected error: %v", err)
	}
	if len(mounted) != 1 || mounted[0].ID != "local/okf" {
		t.Fatalf("mounted = %+v", mounted)
	}
}

func TestPluginUpdate_NotEnabledFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)

	if _, err := PluginUpdate(context.Background(), paths, "local/okf", ""); err == nil {
		t.Fatal("want error for a plugin that is not enabled, got nil")
	}
}

func TestPluginUpdate_PathSourceRejectsRevisionFlag(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	if _, err := PluginUpdate(context.Background(), paths, "local/okf", "v1"); err == nil {
		t.Fatal("want error when --revision is passed for a path-sourced plugin, got nil")
	}
}

func TestPluginUpdate_ReHashesChangedContentAndKeepsSiblingsPinned(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0", "github": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}
	if _, err := PluginAdd(context.Background(), paths, "local/github"); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	githubBefore, _ := lockBefore.FindPlugin("local/github")

	if err := os.WriteFile(filepath.Join(src, "okf", "plugin.toml"), []byte("schema_version = 2\nplect_min_version = \"0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := PluginUpdate(context.Background(), paths, "local/okf", "")
	if err != nil {
		t.Fatalf("PluginUpdate: unexpected error: %v", err)
	}

	lockAfter, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	okfAfter, _ := lockAfter.FindPlugin("local/okf")
	if okfAfter.ContentHash != got.ContentHash {
		t.Errorf("okf ContentHash = %q, want %q", okfAfter.ContentHash, got.ContentHash)
	}
	githubAfter, _ := lockAfter.FindPlugin("local/github")
	if githubAfter.ContentHash != githubBefore.ContentHash {
		t.Error("PluginUpdate must not touch a sibling plugin's lock entry")
	}
}

func TestPluginRemove_NotEnabledFailsLoud(t *testing.T) {
	paths := catalogTestPaths(t)
	if _, err := PluginRemove(paths, "local/okf"); err == nil {
		t.Fatal("want error for a plugin that is not enabled, got nil")
	}
}

func TestPluginRemove_RemovesSelectionAndLock(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	if _, err := PluginRemove(paths, "local/okf"); err != nil {
		t.Fatalf("PluginRemove: unexpected error: %v", err)
	}

	registrations, err := plugins.LoadCatalogRegistrations(paths.CatalogsPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := registrations.Find("local")
	if len(entry.Plugins) != 0 {
		t.Fatalf("enabled plugins = %v, want none", entry.Plugins)
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.FindPlugin("local/okf"); ok {
		t.Fatal("lock entry still present after PluginRemove")
	}
}

func TestPluginVerify_LockedSuccess(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	got, err := PluginVerify(paths, true)
	if err != nil {
		t.Fatalf("PluginVerify: unexpected error: %v", err)
	}
	if !got.AllOK || len(got.Entries) != 1 || !got.Entries[0].OK {
		t.Fatalf("PluginVerify result = %+v", got)
	}
}

func TestPluginVerify_TamperedContentFails(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "okf", "plugin.toml"), []byte("schema_version = 2\nplect_min_version = \"9.9.9\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := PluginVerify(paths, true)
	if err != nil {
		t.Fatalf("PluginVerify: unexpected error: %v", err)
	}
	if got.AllOK || len(got.Entries) != 1 || got.Entries[0].OK {
		t.Fatalf("PluginVerify result = %+v, want a failing entry", got)
	}
}

func TestPluginVerify_LockedOnlySkipsEditableCatalog(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path+editable://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	got, err := PluginVerify(paths, true)
	if err != nil {
		t.Fatalf("PluginVerify: unexpected error: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("PluginVerify(lockedOnly=true) entries = %+v, want none (editable is skipped)", got.Entries)
	}
}

func TestPluginList_ReportsEnabledPlugins(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path+editable://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	got, err := PluginList(paths)
	if err != nil {
		t.Fatalf("PluginList: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "local/okf" || !got[0].NonReproducible || got[0].Status != "ok" {
		t.Errorf("entry = %+v", got[0])
	}
}

// TestPluginAdd_EditableCatalogLockEntryOmitsContentHash guards against
// storing a false reproducibility pin: an editable catalog is never hashed
// or verified, so its lock entry must record Editable=true and no content
// hash, not a silently-always-false Editable flag.
func TestPluginAdd_EditableCatalogLockEntryOmitsContentHash(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path+editable://"+src)

	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.FindPlugin("local/okf")
	if !ok {
		t.Fatal("lock entry not found")
	}
	if !entry.Editable {
		t.Error("Editable = false, want true for a plugin enabled from an editable catalog")
	}
	if entry.ContentHash != "" {
		t.Errorf("ContentHash = %q, want empty for an editable catalog", entry.ContentHash)
	}
}

// TestPluginAdd_RejectsHandEditedSubdirDrift is a regression test:
// PluginAdd's git-catalog fast path (and its non-git fallback) used to read
// catalogs.toml's current `subdir` straight into a fresh fetch and lock
// write, so a hand-edited subdir would get silently trusted the next time
// any plugin from that catalog was enabled — never routing through the
// interactive `plect catalog add` confirmation that changing a trusted
// subtree requires.
func TestPluginAdd_RejectsHandEditedSubdirDrift(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0", "other": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	handEditCatalogSubdir(t, paths, "local", "sub")

	_, err := PluginAdd(context.Background(), paths, "local/other")
	if err == nil || !strings.Contains(err.Error(), "does not match plect.lock") {
		t.Fatalf("PluginAdd after a hand-edited subdir: err = %v, want a source/subdir drift error", err)
	}
}

// TestPluginUpdate_RejectsHandEditedSubdirDrift mirrors
// TestPluginAdd_RejectsHandEditedSubdirDrift for the update path: a
// hand-edited subdir must be rejected before PluginUpdate fetches and
// repoints the lock.
func TestPluginUpdate_RejectsHandEditedSubdirDrift(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"okf": "0.0.0"})
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/okf"); err != nil {
		t.Fatal(err)
	}

	handEditCatalogSubdir(t, paths, "local", "sub")

	_, err := PluginUpdate(context.Background(), paths, "local/okf", "")
	if err == nil || !strings.Contains(err.Error(), "does not match plect.lock") {
		t.Fatalf("PluginUpdate after a hand-edited subdir: err = %v, want a source/subdir drift error", err)
	}
}
