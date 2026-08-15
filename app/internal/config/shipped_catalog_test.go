package config

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/version"
)

// TestShippedCatalog_LoadsAndCompiles guards this repository's own official
// catalog (plugins/catalog.toml plus the plugin directories it lists)
// against a broken manifest or an unparseable task/channel file. This
// is the only place that exercises the catalog end to end: a plugin author
// otherwise only discovers a typo the first time a user runs `plect plugin
// add`.
func TestShippedCatalog_LoadsAndCompiles(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	catalogRoot := filepath.Join(repoRoot, "plugins")

	manifest, err := plugins.LoadCatalogManifest(catalogRoot)
	if err != nil {
		t.Fatalf("LoadCatalogManifest(catalog root): %v", err)
	}
	if len(manifest.Plugins) == 0 {
		t.Fatal("shipped catalog.toml lists no plugins")
	}

	var pluginDirs []string
	// mounted mirrors what a real `plect plugin add official/<path>` mount
	// would produce (catalog alias "official", the actual manifest) so
	// {{bin ...}} references — plugin-local or cross-plugin — resolve
	// exactly as they would at runtime, not just parse.
	var mounted []plugins.Mounted
	for _, rel := range manifest.Plugins {
		dir := filepath.Join(catalogRoot, rel)
		m, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", rel, err)
		}
		ok, err := plugins.AtLeast(version.Current, m.PlectMinVersion)
		if err != nil {
			t.Fatalf("plugin %s: plect_min_version %q: %v", rel, m.PlectMinVersion, err)
		}
		if !ok {
			t.Errorf("plugin %s requires plect >= %s, running %s", rel, m.PlectMinVersion, version.Current)
		}
		pluginDirs = append(pluginDirs, dir)
		mounted = append(mounted, plugins.Mounted{ID: "official/" + rel, Dir: dir, Manifest: m})
	}

	cfg := &Config{PluginDirs: pluginDirs, Plugins: mounted}
	tasks, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions(shipped catalog): %v", err)
	}
	channels, err := cfg.LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	if _, err := cfg.LoadProviders(); err != nil {
		t.Fatalf("LoadProviders(shipped catalog): %v", err)
	}
	if _, err := cfg.LoadResourceDefs(); err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}

	for _, id := range []string{"tmux", "initial_prompt", "claude", "codex", "codex_exec"} {
		if _, ok := tasks[id]; !ok {
			t.Errorf("shipped catalog task %q not found", id)
		}
	}
	for _, id := range []string{"tmux_send_keys", "claude", "codex_exec", "slack"} {
		if _, ok := channels[id]; !ok {
			t.Errorf("shipped catalog channel %q not found", id)
		}
	}
}
