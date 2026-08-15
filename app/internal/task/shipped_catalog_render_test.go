package task

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// TestShippedCatalog_TasksRender guards this repository's own official
// catalog against a broken template: a `{{bin ...}}` reference that doesn't
// resolve, or a `{{...}}` action that fails to parse, would otherwise only
// surface the first time a real session runs the task. A kitchen-sink
// context supplies every key any shipped task's setup/cleanup/healthcheck
// references, so this exercises actual rendering, not just TOML decoding.
func TestShippedCatalog_TasksRender(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	manifest, err := plugins.LoadCatalogManifest(repoRoot)
	if err != nil {
		t.Fatalf("LoadCatalogManifest(repo root): %v", err)
	}

	var pluginDirs []string
	var mounted []plugins.Mounted
	for _, rel := range manifest.Plugins {
		dir := filepath.Join(repoRoot, rel)
		m, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", rel, err)
		}
		id := "official/" + strings.TrimPrefix(rel, "plugins/")
		mounted = append(mounted, plugins.Mounted{ID: id, Dir: dir, Manifest: m})
		pluginDirs = append(pluginDirs, dir)
	}

	cfg := &config.Config{PluginDirs: pluginDirs}
	tasks, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions(shipped catalog): %v", err)
	}

	session := SessionVars{
		Name:          "test-session",
		ResourceID:    "owner:test",
		WorkdirPath:   "/tmp/test-workdir",
		Plugins:       mounted,
		ParentSession: "",
		Inputs:        map[string]any{},
	}

	// Superset of every key a shipped task's setup/cleanup/healthcheck reads
	// off .Prev/.Self, plus every input a shipped task declares.
	kitchen := map[string]any{
		"session_id":     "11111111-1111-1111-1111-111111111111",
		"pid":            12345,
		"socket_path":    "/tmp/claude-channel/x.sock",
		"mcp_config":     "/tmp/plect-mcp.json",
		"hooks_settings": "/tmp/plect-hooks.json",
		"profile_path":   "/tmp/plect-x.config.toml",
		"queue_dir":      "/tmp/plect-codex-exec/test-session/queue",
		"state_dir":      "/tmp/plect-codex-exec/test-session",
		"sent":           "true",
		"session_name":   "test-session",
	}
	inputs := map[string]any{
		"tmux_session":  "test-session",
		"model":         "fable",
		"effort":        "high",
		"template":      "",
		"agent_session": "",
		"repeat":        "",
	}

	for id, def := range tasks {
		ctx := RenderContext{
			Self:    kitchen,
			Prev:    kitchen,
			Inputs:  inputs,
			Session: session,
		}
		if def.Setup != "" {
			if _, err := render(def.Setup, ctx); err != nil {
				t.Errorf("task %q setup: render: %v", id, err)
			}
		}
		if def.Cleanup != "" {
			if _, err := renderCleanup(def.Cleanup, ctx); err != nil {
				t.Errorf("task %q cleanup: render: %v", id, err)
			}
		}
		if def.Healthcheck != "" {
			if _, err := render(def.Healthcheck, ctx); err != nil {
				t.Errorf("task %q healthcheck: render: %v", id, err)
			}
		}
	}
}
