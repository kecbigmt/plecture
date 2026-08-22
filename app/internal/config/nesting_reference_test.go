package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// nestingCatalog stands up a one-plugin editable catalog plus a user global
// layer, and returns the loaded config. enabled names the catalog plugins the
// user turned on; userTasks are the global-layer `tasks/*.toml` bodies.
func nestingCatalog(t *testing.T, published, enabled []string, pluginTasks, userTasks map[string]string) *Config {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	catalogDir := t.TempDir()
	writeFile(t, filepath.Join(catalogDir, "catalog.toml"), "schema_version = 1\nplugins = [\""+strings.Join(published, "\", \"")+"\"]\n")
	for _, p := range published {
		writeFile(t, filepath.Join(catalogDir, p, "plugin.toml"), `
schema_version = 1
plect_min_version = "0.0.0"
`)
	}
	for path, body := range pluginTasks {
		writeFile(t, filepath.Join(catalogDir, filepath.FromSlash(path)), body)
	}

	writeCatalogsToml(t, tmpHome, `
schema_version = 1

[[catalogs]]
alias = "local"
source = "path+editable://`+catalogDir+`"
plugins = ["`+strings.Join(enabled, "\", \"")+`"]
`)
	base := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(base, "config.toml"), "")
	for id, body := range userTasks {
		writeFile(t, filepath.Join(base, "tasks", id+".toml"), body)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// TestLoadTaskDefinitions_QualifiedInnerBypassesUserShadow covers the
// catalog-qualified `inner` form: it selects the task inside the enabled
// plugin, which is the whole point of writing the qualified form when a
// same-id user task exists.
func TestLoadTaskDefinitions_QualifiedInnerBypassesUserShadow(t *testing.T) {
	cfg := nestingCatalog(t,
		[]string{"claude"}, []string{"claude"},
		map[string]string{"claude/config/tasks/claude.toml": innerRuntime},
		map[string]string{
			"claude": `
[claude]
kind = "effect"
scope = "run"

[claude.setup]
type   = "shell"
script = "echo shadow"
`,
			"myclaude": `
[myclaude]
kind = "effect"

[myclaude.inner]
uses = "local/claude/claude"
`,
		},
	)
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	chain := defs["myclaude"].InnerChain
	if len(chain) != 1 {
		t.Fatalf("InnerChain = %v, want one layer", layerIDs(chain))
	}
	if !strings.Contains(chain[0].SourcePath, filepath.Join("claude", "config", "tasks")) {
		t.Errorf("inner resolved to %s, want the plugin's own task file", chain[0].SourcePath)
	}
}

// TestLoadTaskDefinitions_PluginInnerResolvesInItsOwnNamespace covers the
// relative form inside plugin-authored config: it must reach the plugin's own
// task even when a user task of the same id shadows it in the merged
// namespace, so a user shadow can never hijack a plugin's own composition.
func TestLoadTaskDefinitions_PluginInnerResolvesInItsOwnNamespace(t *testing.T) {
	cfg := nestingCatalog(t,
		[]string{"claude"}, []string{"claude"},
		map[string]string{
			"claude/config/tasks/claude.toml": innerRuntime,
			"claude/config/tasks/wrapper.toml": `
[wrapper]
kind = "effect"

[wrapper.inner]
uses = "claude"
`,
		},
		map[string]string{
			"claude": `
[claude]
kind = "effect"
scope = "run"

[claude.setup]
type   = "shell"
script = "echo shadow"
`,
		},
	)
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
	chain := defs["wrapper"].InnerChain
	if len(chain) != 1 {
		t.Fatalf("InnerChain = %v, want one layer", layerIDs(chain))
	}
	if !strings.Contains(chain[0].SourcePath, filepath.Join("claude", "config", "tasks")) {
		t.Errorf("inner resolved to %s, want the plugin's own task file", chain[0].SourcePath)
	}
}

func TestLoadTaskDefinitions_QualifiedInnerReferenceErrors(t *testing.T) {
	tests := []struct {
		name      string
		published []string
		enabled   []string
		inner     string
		wantErr   string
	}{
		{
			name:      "an unknown catalog alias",
			published: []string{"claude"},
			enabled:   []string{"claude"},
			inner:     "elsewhere/claude/claude",
			wantErr:   "elsewhere/claude/claude",
		},
		{
			name:      "a plugin the user has not enabled",
			published: []string{"claude", "codex"},
			enabled:   []string{"claude"},
			inner:     "local/codex/codex",
			wantErr:   "plect plugin add local/codex",
		},
		{
			name:      "a task missing from the selected plugin",
			published: []string{"claude"},
			enabled:   []string{"claude"},
			inner:     "local/claude/codex",
			wantErr:   "local/claude/codex",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := nestingCatalog(t,
				tt.published, tt.enabled,
				map[string]string{"claude/config/tasks/claude.toml": innerRuntime},
				map[string]string{"myclaude": `
[myclaude]
kind = "effect"

[myclaude.inner]
uses = "` + tt.inner + `"
`},
			)
			_, err := cfg.LoadTaskDefinitions("")
			if err == nil {
				t.Fatalf("LoadTaskDefinitions: want an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("LoadTaskDefinitions error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
