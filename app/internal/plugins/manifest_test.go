package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
version = "0.3.0"
plect_min_version = "0.8.0"
description = "GitHub resource provider and workflow support."

[[executables]]
name = "plect-github-watcher"
path = "bin/plect-github-watcher"
build = "go build -o bin/plect-github-watcher ./cmd/plect-github-watcher"
`)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: unexpected error: %v", err)
	}
	if m.SchemaVersion != 2 || m.Version != "0.3.0" || m.PlectMinVersion != "0.8.0" {
		t.Fatalf("Manifest = %+v", m)
	}
	if len(m.Executables) != 1 || m.Executables[0].Name != "plect-github-watcher" || m.Executables[0].Path != "bin/plect-github-watcher" {
		t.Fatalf("Executables = %+v", m.Executables)
	}
}

func TestLoadManifest_VersionAndDescriptionOptional(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"
`)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: unexpected error: %v", err)
	}
	if m.Version != "" || m.Description != "" {
		t.Fatalf("Manifest = %+v", m)
	}
}

func TestLoadManifest_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for missing plugin.toml, got nil")
	}
}

func TestLoadManifest_MissingSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `plect_min_version = "0.1.0"`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for missing schema_version, got nil")
	}
}

func TestLoadManifest_UnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 99
plect_min_version = "0.1.0"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for an unknown schema_version, got nil")
	}
}

func TestLoadManifest_MissingPlectMinVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `schema_version = 2`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for missing plect_min_version, got nil")
	}
}

func TestLoadManifest_MalformedPlectMinVersion(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "not-a-version"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for malformed plect_min_version, got nil")
	}
}

func TestLoadManifest_ExecutableMissingName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
path = "bin/x"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for executable missing name, got nil")
	}
}

func TestLoadManifest_ExecutableMissingPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for executable missing path, got nil")
	}
}

func TestLoadManifest_DuplicateExecutableName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[executables]]
name = "x"
path = "bin/x2"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for duplicate executable name, got nil")
	}
}

func TestLoadManifest_ServiceValid(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "channel-server"
path = "bin/channel-server"

[[services]]
name = "channel-server"
executable = "channel-server"
`)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: unexpected error: %v", err)
	}
	if len(m.Services) != 1 {
		t.Fatalf("Services = %+v", m.Services)
	}
	svc := m.Services[0]
	if svc.Name != "channel-server" || svc.Executable != "channel-server" {
		t.Fatalf("Service = %+v", svc)
	}
	if svc.Restart != ServiceRestartOnFailure {
		t.Fatalf("Restart default = %q, want %q", svc.Restart, ServiceRestartOnFailure)
	}
	if svc.Health.Type != ServiceHealthProcess {
		t.Fatalf("Health.Type default = %q, want %q", svc.Health.Type, ServiceHealthProcess)
	}
}

func TestLoadManifest_ServiceExplicitFields(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "slack-adapter"
path = "bin/slack-adapter"

[[services]]
name = "slack-adapter"
executable = "slack-adapter"
args = ["serve"]
env = { LOG_LEVEL = "info" }
required_env = ["SLACK_BOT_TOKEN"]
restart = "never"
health = { type = "process" }
`)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: unexpected error: %v", err)
	}
	svc := m.Services[0]
	if len(svc.Args) != 1 || svc.Args[0] != "serve" {
		t.Fatalf("Args = %+v", svc.Args)
	}
	if svc.Env["LOG_LEVEL"] != "info" {
		t.Fatalf("Env = %+v", svc.Env)
	}
	if len(svc.RequiredEnv) != 1 || svc.RequiredEnv[0] != "SLACK_BOT_TOKEN" {
		t.Fatalf("RequiredEnv = %+v", svc.RequiredEnv)
	}
	if svc.Restart != ServiceRestartNever {
		t.Fatalf("Restart = %q, want %q", svc.Restart, ServiceRestartNever)
	}
}

func TestLoadManifest_ServiceMissingName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[services]]
executable = "x"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for service missing name, got nil")
	}
}

func TestLoadManifest_ServiceMissingExecutable(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[services]]
name = "x"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for service missing executable, got nil")
	}
}

func TestLoadManifest_ServiceExecutableNotDeclared(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[services]]
name = "svc"
executable = "y"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for service executable not declared by this plugin, got nil")
	}
}

func TestLoadManifest_DuplicateServiceName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[services]]
name = "svc"
executable = "x"

[[services]]
name = "svc"
executable = "x"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for duplicate service name, got nil")
	}
}

func TestLoadManifest_ServiceInvalidRestartPolicy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[services]]
name = "svc"
executable = "x"
restart = "always"
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for unsupported restart policy, got nil")
	}
}

func TestLoadManifest_ServiceInvalidHealthType(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
schema_version = 2
plect_min_version = "0.1.0"

[[executables]]
name = "x"
path = "bin/x"

[[services]]
name = "svc"
executable = "x"
health = { type = "http" }
`)

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("want error for unsupported health type, got nil")
	}
}
