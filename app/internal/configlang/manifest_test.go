package configlang

import "testing"

func TestValidatePluginManifestValid(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "GitHub workspace provider, resource observation, watcher subscription, and the work/review/respond/investigate task pack."

[[executables]]
name  = "github-worktree"
path  = "bin/github-worktree"
build = "go -C src build -o ../bin/github-worktree ./cmd/github-worktree"

[[executables]]
name = "gh-guard"
path = "scripts/gh-guard"

[[executables]]
name = "github-watcher"
path = "bin/github-watcher"

[[services]]
name       = "github-watcher"
executable = "github-watcher"
args       = ["serve"]
restart    = "on-failure"
health     = { type = "process" }
`)
	m, err := ValidatePluginManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Executables) != 3 || len(m.Services) != 1 {
		t.Fatalf("unexpected decode: %+v", m)
	}
}

func TestValidatePluginManifestMissingSchemaVersion(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "A manifest with no schema_version."
`)
	_, err := ValidatePluginManifest(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestValidatePluginManifestMissingVersion(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
plect_min_version = "0.0.0"
description       = "A manifest with no version."
`)
	_, err := ValidatePluginManifest(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestValidatePluginManifestMissingDescription(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "0.0.0"
`)
	_, err := ValidatePluginManifest(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestValidatePluginManifestUnknownField(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "d"
homepage          = "https://example.invalid"
`)
	_, err := ValidatePluginManifest(path)
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
}

func TestValidatePluginManifestDuplicateExecutableName(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "A plugin declaring the same executable name twice."

[[executables]]
name = "github-worktree"
path = "bin/github-worktree"

[[executables]]
name = "github-worktree"
path = "scripts/github-worktree"
`)
	_, err := ValidatePluginManifest(path)
	if err == nil {
		t.Fatal("expected an error for a duplicate executable name")
	}
}

func TestValidatePluginManifestPlectMinVersionExceedsRunning(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "999.0.0"
description       = "A plugin that requires a plect version far newer than any that exists."
`)
	_, err := ValidatePluginManifest(path)
	if err == nil {
		t.Fatal("expected an error: plect_min_version exceeds the running plect")
	}
}

func TestValidatePluginManifestServiceUnknownExecutable(t *testing.T) {
	path := writeTemp(t, "plugin.toml", `
schema_version    = 1
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "A plugin whose service names an executable it does not declare."

[[executables]]
name = "github-worktree"
path = "bin/github-worktree"

[[services]]
name       = "github-watcher"
executable = "github-watcher"
restart    = "on-failure"
health     = { type = "process" }
`)
	_, err := ValidatePluginManifest(path)
	assertDiagnostic(t, err, CodeUnknownRef, LayerSemantic)
}

func TestValidateCatalogManifestValid(t *testing.T) {
	path := writeTemp(t, "catalog.toml", `
schema_version = 1
description    = "Plecture's official plugin catalog."

plugins = ["tmux", "claude", "codex", "slack", "okf", "github"]
`)
	m, err := ValidateCatalogManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Plugins) != 6 {
		t.Fatalf("unexpected decode: %+v", m)
	}
}

func TestValidateCatalogManifestMissingSchemaVersion(t *testing.T) {
	path := writeTemp(t, "catalog.toml", `plugins = ["tmux"]`)
	_, err := ValidateCatalogManifest(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestValidateCatalogManifestMissingPlugins(t *testing.T) {
	path := writeTemp(t, "catalog.toml", `schema_version = 1`)
	_, err := ValidateCatalogManifest(path)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

// TestValidatePluginManifestAgainstShippedManifests is a smoke test that the
// new closed-surface validator accepts the plugins this repository actually
// ships, so this slice's structural rules do not silently diverge from
// content nothing has migrated yet (they are not wired into any runtime
// surface — see the package doc comment).
func TestValidatePluginManifestAgainstShippedManifests(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"claude", "codex", "github", "okf", "slack", "tmux"} {
		path := root + "/plugins/" + name + "/plugin.toml"
		if _, err := ValidatePluginManifest(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestValidateCatalogManifestAgainstShippedCatalog(t *testing.T) {
	root := repoRoot(t)
	if _, err := ValidateCatalogManifest(root + "/plugins/catalog.toml"); err != nil {
		t.Errorf("plugins/catalog.toml: %v", err)
	}
}
