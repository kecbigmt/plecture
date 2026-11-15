package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestPluginUpdateReportsBeforeBuildAndDryRunDoesNotApply(t *testing.T) {
	paths := catalogTestPaths(t)
	src := writeCatalogSource(t, map[string]string{"work": "0.0.0"})
	pluginDir := filepath.Join(src, "work")
	marker := filepath.Join(t.TempDir(), "build-ran")
	manifest := "schema_version = 3\nplect_min_version = \"0.0.0\"\n[[executables]]\nname = \"worker\"\npath = \"bin/worker\"\nservice = true\nbuild = \"mkdir -p bin && printf built > bin/worker && printf ran > " + marker + "\"\n[executables.config_schema]\ntype = \"object\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"scripts/helper.sh":    "#!/bin/sh\ntrue\n",
		"config/settings.toml": "enabled = true\n",
		"prompts/guide.md":     "First version.\n",
	} {
		path := filepath.Join(pluginDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addTestCatalog(t, paths, "local", "path://"+src)
	if _, err := PluginAdd(context.Background(), paths, "local/work"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	serviceDir := filepath.Join(filepath.Dir(paths.CatalogsPath), "services")
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(serviceDir, "worker.env")
	if err := os.WriteFile(envPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	declaration := "[[services]]\nname = \"worker-service\"\nexecutable = \"local/work/worker\"\nenv_file = \"" + envPath + "\"\n"
	if err := os.WriteFile(filepath.Join(serviceDir, "worker.toml"), []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "prompts/guide.md"), []byte("Second version.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}

	var dryPreview PluginUpdatePreview
	_, err = PluginUpdate(context.Background(), paths, "local/work", PluginUpdateParams{DryRun: true, OnPreview: func(preview PluginUpdatePreview) error {
		dryPreview = preview
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("build ran before dry-run preview: %v", err)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	lockAfterDryRun, err := os.ReadFile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lockBefore, lockAfterDryRun) {
		t.Fatal("dry-run changed plect.lock")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("dry-run ran build: %v", err)
	}
	if !dryPreview.OldTreeUnavailable || !reflect.DeepEqual(dryPreview.ServicesToRestart, []string{"worker-service"}) {
		t.Fatalf("dry-run preview = %+v", dryPreview)
	}
	for _, kind := range []string{"scripts and sources", "toml", "prompts and docs"} {
		if !strings.Contains(strings.Join(previewKinds(dryPreview.Changes), ","), kind) {
			t.Fatalf("preview kinds = %v, missing %q", previewKinds(dryPreview.Changes), kind)
		}
	}
	var realPreview PluginUpdatePreview
	_, err = PluginUpdate(context.Background(), paths, "local/work", PluginUpdateParams{OnPreview: func(preview PluginUpdatePreview) error {
		realPreview = preview
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("build ran before update preview: %v", err)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dryPreview, realPreview) {
		t.Fatalf("dry-run preview = %+v; update preview = %+v", dryPreview, realPreview)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("update did not run build: %v", err)
	}
	lockAfterUpdate, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := lockAfterUpdate.FindPlugin("local/work")
	if entry.ContentHash == "" {
		t.Fatal("update did not persist plugin content hash")
	}
}

func previewKinds(changes []PluginFileChange) []string {
	var kinds []string
	for _, change := range changes {
		kinds = append(kinds, change.Kind)
	}
	return kinds
}

func TestComparePluginTreesReportsAddedModifiedAndRemovedFiles(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	for dir, files := range map[string]map[string]string{
		oldDir: {"plugin.toml": "v1", "scripts/modified.sh": "old", "docs/removed.md": "old", "bin/generated": "old build"},
		newDir: {"plugin.toml": "v1", "scripts/modified.sh": "new", "config/added.toml": "new", "bin/generated": "new build"},
	} {
		for name, body := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	changes, err := comparePluginTrees(oldDir, newDir, []string{"bin/generated"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []PluginFileChange{
		{Path: "scripts/modified.sh", Kind: "scripts and sources", Change: "modified"},
		{Path: "config/added.toml", Kind: "toml", Change: "added"},
		{Path: "docs/removed.md", Kind: "prompts and docs", Change: "removed"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
}

func TestPluginUpdateComparesPinnedSnapshotsAndReportsMissingOldTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	paths := catalogTestPaths(t)
	repo := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=plect-test", "GIT_AUTHOR_EMAIL=plect-test@example.test", "GIT_COMMITTER_NAME=plect-test", "GIT_COMMITTER_EMAIL=plect-test@example.test")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	write("catalog.toml", "schema_version = 2\nplugins = [\"work\"]\n")
	write("work/plugin.toml", "schema_version = 3\nplect_min_version = \"0.0.0\"\n[[executables]]\nname = \"worker\"\npath = \"bin/worker\"\nservice = true\n[executables.config_schema]\ntype = \"object\"\n")
	write("work/bin/worker", "#!/bin/sh\necho old\n")
	write("work/scripts/helper.sh", "#!/bin/sh\necho old\n")
	write("work/docs/old.md", "Old.\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "first")
	first := git("rev-parse", "HEAD")
	source := "git+https://catalog.invalid/work"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://catalog.invalid/work")
	params := CatalogAddParams{Alias: "local", Source: source, Revision: first}
	_, fetched, err := PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CommitCatalogAdd(paths, params, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := PluginAdd(context.Background(), paths, "local/work"); err != nil {
		t.Fatal(err)
	}
	write("work/bin/worker", "#!/bin/sh\necho new\n")
	write("work/scripts/helper.sh", "#!/bin/sh\necho new\n")
	write("work/config/settings.toml", "enabled = true\n")
	write("work/prompts/new.md", "New.\n")
	if err := os.Remove(filepath.Join(repo, "work/docs/old.md")); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "second")
	second := git("rev-parse", "HEAD")

	var preview PluginUpdatePreview
	_, err = PluginUpdate(context.Background(), paths, "local/work", PluginUpdateParams{Revision: second, DryRun: true, OnPreview: func(p PluginUpdatePreview) error { preview = p; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if preview.OldTreeUnavailable {
		t.Fatal("old git snapshot was available")
	}
	want := []PluginFileChange{
		{Path: "bin/worker", Kind: "executables", Change: "modified"},
		{Path: "scripts/helper.sh", Kind: "scripts and sources", Change: "modified"},
		{Path: "config/settings.toml", Kind: "toml", Change: "added"},
		{Path: "docs/old.md", Kind: "prompts and docs", Change: "removed"},
		{Path: "prompts/new.md", Kind: "prompts and docs", Change: "added"},
	}
	if !reflect.DeepEqual(preview.Changes, want) {
		t.Fatalf("git preview changes = %+v, want %+v", preview.Changes, want)
	}
	if err := os.RemoveAll(plugins.CacheDir(paths.CacheRoot, source, first)); err != nil {
		t.Fatal(err)
	}
	_, err = PluginUpdate(context.Background(), paths, "local/work", PluginUpdateParams{Revision: second, DryRun: true, OnPreview: func(p PluginUpdatePreview) error { preview = p; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.OldTreeUnavailable || len(preview.Changes) != 5 {
		t.Fatalf("missing old tree preview = %+v", preview)
	}
	for _, change := range preview.Changes {
		if change.Change != "changed" {
			t.Fatalf("missing old tree change = %+v, want changed", change)
		}
	}
}
