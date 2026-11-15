package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestWritePluginUpdatePreviewGroupsFilesByKind(t *testing.T) {
	preview := service.PluginUpdatePreview{ID: "local/work", Changes: []service.PluginFileChange{
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
