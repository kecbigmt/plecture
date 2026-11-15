package commands

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestWritePluginUpdatePreviewGroupsFilesByKind(t *testing.T) {
	preview := service.PluginUpdatePreview{ID: "local/work", ResolvedRevision: "abc123", Changes: []service.PluginFileChange{
		{Path: "bin/worker", Kind: "executables", Change: "modified"},
		{Path: "scripts/helper.sh", Kind: "scripts and sources", Change: "added"},
		{Path: "plugin.toml", Kind: "toml", Change: "modified"},
		{Path: "README.md", Kind: "prompts and docs", Change: "removed"},
	}, ServicesToRestart: []string{"worker-service"}}
	var out bytes.Buffer
	if err := writePluginUpdatePreview(&out, preview); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	before := -1
	for _, heading := range []string{"executables:", "scripts and sources:", "toml:", "prompts and docs:", "Services to restart:"} {
		index := strings.Index(text, heading)
		if index <= before {
			t.Fatalf("preview headings out of order: %q", text)
		}
		before = index
	}
	if !strings.Contains(text, "worker-service") {
		t.Fatalf("preview omitted restarting service: %q", text)
	}
	if !strings.Contains(text, "Target revision: abc123") {
		t.Fatalf("preview omitted target revision: %q", text)
	}
}

func TestPluginUpdateDryRunShowsChangesWithoutChangingLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configHome, cacheHome := t.TempDir(), t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("PLECT_CONFIG_HOME", configHome)
	t.Setenv("PLECT_CACHE_HOME", cacheHome)
	t.Setenv("PLECT_DATA_HOME", dataHome)
	t.Cleanup(func() { pluginUpdateDryRun = false; dataHomeFlag = "" })
	paths := service.PluginPaths{
		CatalogsPath: filepath.Join(configHome, "catalogs.toml"),
		LockfilePath: filepath.Join(configHome, "plect.lock"),
		CacheRoot:    filepath.Join(cacheHome, "catalogs"),
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "catalog.toml"), []byte("schema_version = 2\nplugins = [\"work\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(src, "work")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte("schema_version = 3\nplect_min_version = \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params := service.CatalogAddParams{Alias: "local", Source: "path://" + src}
	_, fetched, err := service.PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CommitCatalogAdd(paths, params, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PluginAdd(context.Background(), paths, "local/work"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "README.md"), []byte("Updated.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "--data-home", dataHome, "plugin", "update", "local/work", "--dry-run")
	if err != nil {
		t.Fatalf("plugin update --dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"Previous plugin tree unavailable", "prompts and docs:", "changed README.md", "Services to restart:", "dry-run: no changes made"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}
	after, err := os.ReadFile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("plugin update --dry-run changed plect.lock")
	}
	files, err := os.ReadDir(dataHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("plugin update --dry-run wrote data-home files: %v", files)
	}
}

func TestPluginUpdateInstallsRevisionPrintedByDryRunAfterUpstreamAdvances(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	t.Setenv("HOME", t.TempDir())
	configHome, cacheHome, dataHome := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PLECT_CONFIG_HOME", configHome)
	t.Setenv("PLECT_CACHE_HOME", cacheHome)
	t.Setenv("PLECT_DATA_HOME", dataHome)
	t.Cleanup(func() { pluginUpdateDryRun = false; pluginUpdateRevision = ""; dataHomeFlag = "" })
	paths := service.PluginPaths{
		CatalogsPath: filepath.Join(configHome, "catalogs.toml"),
		LockfilePath: filepath.Join(configHome, "plect.lock"),
		CacheRoot:    filepath.Join(cacheHome, "catalogs"),
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=plect-test", "GIT_AUTHOR_EMAIL=plect-test@example.test", "GIT_COMMITTER_NAME=plect-test", "GIT_COMMITTER_EMAIL=plect-test@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	write("catalog.toml", "schema_version = 2\nplugins = [\"work\"]\n")
	write("work/plugin.toml", "schema_version = 3\nplect_min_version = \"0.0.0\"\n")
	write("work/README.md", "first\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "first")
	first := git("rev-parse", "HEAD")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+repo+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://catalog.invalid/work")
	params := service.CatalogAddParams{Alias: "local", Source: "git+https://catalog.invalid/work", Revision: first}
	_, fetched, err := service.PreviewCatalogAdd(context.Background(), paths, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CommitCatalogAdd(paths, params, fetched); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PluginAdd(context.Background(), paths, "local/work"); err != nil {
		t.Fatal(err)
	}
	write("work/README.md", "second\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "second")
	second := git("rev-parse", "HEAD")
	out, err := execRoot(t, "--data-home", dataHome, "plugin", "update", "local/work", "--revision", second, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Target revision: "+second) {
		t.Fatalf("dry run omitted resolved revision %q: %s", second, out)
	}
	write("work/README.md", "third\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "third")
	pluginUpdateDryRun = false
	out, err = execRoot(t, "--data-home", dataHome, "plugin", "update", "local/work", "--revision", second)
	if err != nil {
		t.Fatalf("pinned update: %v\n%s", err, out)
	}
	lock, err := plugins.LoadLockfile(paths.LockfilePath)
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := lock.FindPlugin("local/work")
	if !ok || installed.CatalogResolvedRevision != second {
		t.Fatalf("installed plugin = %+v, want revision %q", installed, second)
	}
}
