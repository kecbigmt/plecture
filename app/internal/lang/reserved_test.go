package lang

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadConfigTomlValid(t *testing.T) {
	path := writeTemp(t, "config.toml", `
schema_version = 2
workspace_dirs_root = "~/worktrees"
resource_allowlist  = ["^https://github\\.com/kecbigmt/"]
plugin_dirs         = ["~/.config/plect/plugins"]
channels            = ["notify"]

[inputs_schema]
type = "object"
`)
	cfg, err := LoadConfigToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchemaVersion != 2 || cfg.WorkspaceDirsRoot != "~/worktrees" {
		t.Fatalf("unexpected decode: %+v", cfg)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0] != "notify" {
		t.Fatalf("unexpected channels: %+v", cfg.Channels)
	}
}

func TestLoadConfigTomlMissingSchemaVersion(t *testing.T) {
	path := writeTemp(t, "config.toml", `workspace_dirs_root = "~/worktrees"`)
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestLoadConfigTomlUnknownField(t *testing.T) {
	path := writeTemp(t, "config.toml", `
schema_version = 2
worktrees_root = "~/worktrees"
`)
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
}

func TestLoadConfigTomlRejectsNonPositiveVirtualRootCap(t *testing.T) {
	path := writeTemp(t, "config.toml", "schema_version = 2\nmax_up_children = 0\n")
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeFieldType, LayerStructural)
}

func TestLoadConfigTomlDefinitionTableIsUnknownField(t *testing.T) {
	path := writeTemp(t, "config.toml", `
schema_version = 2
workspace_dirs_root = "~/worktrees"

[runtime]
kind = "effect"
`)
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
}

func TestLoadConfigTomlSchemaVersionOlder(t *testing.T) {
	path := writeTemp(t, "config.toml", `
schema_version = 1
workspace_dirs_root = "~/worktrees"
`)
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeSchemaVersionOlder, LayerSemantic)
}

func TestLoadConfigTomlSchemaVersionNewer(t *testing.T) {
	path := writeTemp(t, "config.toml", `
schema_version = 3
workspace_dirs_root = "~/worktrees"
`)
	_, err := LoadConfigToml(path)
	assertDiagnostic(t, err, CodeSchemaVersionNewer, LayerSemantic)
}

func TestLoadCatalogsTomlValid(t *testing.T) {
	path := writeTemp(t, "catalogs.toml", `
schema_version = 2

[[catalogs]]
alias   = "official"
source  = "https://github.com/kecbigmt/plecture"
subdir  = "plugins"
plugins = ["tmux", "claude", "github"]
`)
	cat, err := LoadCatalogsToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cat.Catalogs) != 1 || cat.Catalogs[0].Alias != "official" {
		t.Fatalf("unexpected decode: %+v", cat)
	}
}

func TestLoadCatalogsTomlMissingSchemaVersion(t *testing.T) {
	path := writeTemp(t, "catalogs.toml", `
[[catalogs]]
alias  = "official"
source = "https://github.com/kecbigmt/plecture"
`)
	_, err := LoadCatalogsToml(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestLoadLockTomlValid(t *testing.T) {
	path := writeTemp(t, "plect.lock", `
schema_version = 2

[[plugins]]
id                = "official/github"
catalog_alias     = "official"
path              = "/home/dev/.cache/plect/official/github"
content_hash      = "sha256:abc"
`)
	lock, err := LoadLockToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lock.Plugins) != 1 || lock.Plugins[0].ID != "official/github" {
		t.Fatalf("unexpected decode: %+v", lock)
	}
}

func TestLoadLockTomlSchemaVersionNewer(t *testing.T) {
	path := writeTemp(t, "plect.lock", `schema_version = 3`)
	_, err := LoadLockToml(path)
	assertDiagnostic(t, err, CodeSchemaVersionNewer, LayerSemantic)
}

func assertDiagnostic(t *testing.T, err error, code Code, layer Layer) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var d *Diagnostic
	if !errors.As(err, &d) {
		t.Fatalf("expected a *Diagnostic, got %T: %v", err, err)
	}
	if d.Code != code {
		t.Errorf("code = %s, want %s", d.Code, code)
	}
	if d.Layer != layer {
		t.Errorf("layer = %s, want %s", d.Layer, layer)
	}
}
