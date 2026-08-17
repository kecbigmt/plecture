package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/confighome"
)

// writeInitCatalogFixture builds a minimal path+editable:// catalog on disk
// (a catalog.toml manifest plus one plugin.toml per published path) so init
// tests never need network access.
func writeInitCatalogFixture(t *testing.T, pluginPaths ...string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "schema_version = 1\nplugins = [" + quoteJoin(pluginPaths) + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "catalog.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range pluginPaths {
		pluginDir := filepath.Join(dir, p)
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "schema_version = 1\nplect_min_version = \"0.0.0\"\n"
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writeInitCatalogFixtureWithSubdir is writeInitCatalogFixture with the
// catalog.toml and plugin directories nested one level under dirName, so
// tests can exercise `plect init --catalog-subdir`.
func writeInitCatalogFixtureWithSubdir(t *testing.T, dirName string, pluginPaths ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version = 1\nplugins = [" + quoteJoin(pluginPaths) + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "catalog.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range pluginPaths {
		pluginDir := filepath.Join(dir, p)
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "schema_version = 1\nplect_min_version = \"0.0.0\"\n"
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func quoteJoin(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// resetInitFlags undoes package-level cobra flag state, which (like
// configHomeFlag) outlives a single Execute() call.
func resetInitFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		initCatalogAlias, initCatalogSource, initCatalogSubdir, initCatalogRevision, initWorkspaceDirsRoot = "", "", "", "", ""
		initEnablePlugins, initAllowlist = nil, nil
		initYes, initAllowAll = false, false
	})
}

func setInitConfigHome(t *testing.T) string {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(confighome.EnvVar, "")
	return filepath.Join(fakeHome, ".config", "plect")
}

func TestInit_YesBootstrapsFreshConfigHome(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude", "channel/slack")
	workspaceDirs := filepath.Join(t.TempDir(), "work")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "agent/claude",
		"--enable", "channel/slack",
		"--allowlist", "^acme/",
		"--workspace-dirs-root", workspaceDirs,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}

	configPath := filepath.Join(configHome, "config.toml")
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("config.toml not written: %v", readErr)
	}
	if !strings.Contains(string(data), workspaceDirs) {
		t.Errorf("config.toml missing workspaceDirs root:\n%s", data)
	}
	if !strings.Contains(string(data), "^acme/") {
		t.Errorf("config.toml missing allowlist pattern:\n%s", data)
	}

	catalogsData, err := os.ReadFile(filepath.Join(configHome, "catalogs.toml"))
	if err != nil {
		t.Fatalf("catalogs.toml not written: %v", err)
	}
	if !strings.Contains(string(catalogsData), "agent/claude") || !strings.Contains(string(catalogsData), "channel/slack") {
		t.Errorf("catalogs.toml missing enabled plugins:\n%s", catalogsData)
	}

	// A session should be dispatchable immediately: Load() must resolve the
	// enabled plugins into the live config without any further hand-authored
	// TOML.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() after init: %v", err)
	}
	if len(cfg.Plugins) != 2 {
		t.Errorf("cfg.Plugins = %+v, want 2 resolved plugins", cfg.Plugins)
	}
	if cfg.WorkspaceDirsRoot != workspaceDirs {
		t.Errorf("cfg.WorkspaceDirsRoot = %q, want %q", cfg.WorkspaceDirsRoot, workspaceDirs)
	}
}

func TestInit_Yes_CatalogSubdirScopesTrustSpace(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	root := writeInitCatalogFixtureWithSubdir(t, "plugins", "agent/claude")
	workspaceDirs := filepath.Join(t.TempDir(), "work")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+root,
		"--catalog-subdir", "plugins",
		"--enable", "agent/claude",
		"--allow-all",
		"--workspace-dirs-root", workspaceDirs,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}

	catalogsData, readErr := os.ReadFile(filepath.Join(configHome, "catalogs.toml"))
	if readErr != nil {
		t.Fatalf("catalogs.toml not written: %v", readErr)
	}
	if !strings.Contains(string(catalogsData), "subdir = \"plugins\"") {
		t.Errorf("catalogs.toml missing subdir = \"plugins\":\n%s", catalogsData)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() after init: %v", err)
	}
	if len(cfg.Plugins) != 1 {
		t.Errorf("cfg.Plugins = %+v, want 1 resolved plugin", cfg.Plugins)
	}
}

func TestInit_RefusesWhenConfigTomlAlreadyExists(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "config.toml"), []byte("workspace_dirs_root = \"/existing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := writeInitCatalogFixture(t, "okf")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
	)
	if err == nil {
		t.Fatalf("Execute() succeeded, want refusal; output:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "catalogs.toml")); !os.IsNotExist(statErr) {
		t.Error("init wrote catalogs.toml despite refusing on an already-initialized config home")
	}
}

func TestInit_RefusesWhenCatalogAlreadyRegistered(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatal(err)
	}
	existingCatalogs := `schema_version = 1

[[catalogs]]
alias = "existing"
source = "path:///nonexistent"
plugins = []
`
	if err := os.WriteFile(filepath.Join(configHome, "catalogs.toml"), []byte(existingCatalogs), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := writeInitCatalogFixture(t, "okf")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
	)
	if err == nil {
		t.Fatalf("Execute() succeeded, want refusal; output:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "config.toml")); !os.IsNotExist(statErr) {
		t.Error("init wrote config.toml despite refusing on an already-registered catalog")
	}
}

func TestInit_YesRequiresCatalogFlags(t *testing.T) {
	resetInitFlags(t)
	setInitConfigHome(t)

	out, err := execRoot(t, "init", "--yes")
	if err == nil {
		t.Fatalf("Execute() succeeded without --catalog-alias/--catalog-source, want an error; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--catalog-alias") {
		t.Errorf("error = %v, want it to name the required flags", err)
	}
}

func TestInit_RejectsUnpublishedPluginPath(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "okf")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "not-published",
	)
	if err == nil {
		t.Fatalf("Execute() succeeded enabling an unpublished plugin path; output:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "catalogs.toml")); !os.IsNotExist(statErr) {
		t.Error("init registered the catalog despite an invalid --enable value")
	}
}

// TestInit_YesRequiresExplicitEnable is a regression test: --yes with only
// the catalog flags set used to succeed by silently enabling no plugins,
// leaving a config home registered but with nothing to dispatch.
func TestInit_YesRequiresExplicitEnable(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--allow-all",
		"--workspace-dirs-root", filepath.Join(t.TempDir(), "work"),
	)
	if err == nil {
		t.Fatalf("Execute() succeeded with --yes and no --enable, want an error; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--enable") {
		t.Errorf("error = %v, want it to name --enable as required", err)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "catalogs.toml")); !os.IsNotExist(statErr) {
		t.Error("init registered the catalog despite a missing required --enable answer")
	}
}

// TestInit_YesRequiresExplicitAllowlistOrAllowAll is a regression test:
// --yes used to silently write an allow-all config.toml when --allowlist
// was never passed, even though the allowlist is a security boundary.
func TestInit_YesRequiresExplicitAllowlistOrAllowAll(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "agent/claude",
		"--workspace-dirs-root", filepath.Join(t.TempDir(), "work"),
	)
	if err == nil {
		t.Fatalf("Execute() succeeded with --yes and no --allowlist/--allow-all, want an error; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--allowlist") {
		t.Errorf("error = %v, want it to name --allowlist/--allow-all as required", err)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "catalogs.toml")); !os.IsNotExist(statErr) {
		t.Error("init registered the catalog despite a missing required allowlist answer")
	}
}

func TestInit_YesAllowAllOptsOutOfAllowlistExplicitly(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude")
	workspaceDirs := filepath.Join(t.TempDir(), "work")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "agent/claude",
		"--allow-all",
		"--workspace-dirs-root", workspaceDirs,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	data, readErr := os.ReadFile(filepath.Join(configHome, "config.toml"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(data), "resource_allowlist") {
		t.Errorf("config.toml should have no resource_allowlist key when --allow-all was passed:\n%s", data)
	}
}

func TestInit_YesRejectsAllowlistAndAllowAllTogether(t *testing.T) {
	resetInitFlags(t)
	setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "agent/claude",
		"--allowlist", "^acme/",
		"--allow-all",
		"--workspace-dirs-root", filepath.Join(t.TempDir(), "work"),
	)
	if err == nil {
		t.Fatalf("Execute() succeeded with both --allowlist and --allow-all, want a mutual-exclusivity error; output:\n%s", out)
	}
}

// TestInit_YesRequiresExplicitWorkspaceDirsRoot is a regression test: --yes
// used to silently fall back to ~/workspaceDirs when --workspace-dirs-root was never
// passed.
func TestInit_YesRequiresExplicitWorkspaceDirsRoot(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude")

	out, err := execRoot(t, "init", "--yes",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "agent/claude",
		"--allow-all",
	)
	if err == nil {
		t.Fatalf("Execute() succeeded with --yes and no --workspace-dirs-root, want an error; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--workspace-dirs-root") {
		t.Errorf("error = %v, want it to name --workspace-dirs-root as required", err)
	}
	if _, statErr := os.Stat(filepath.Join(configHome, "catalogs.toml")); !os.IsNotExist(statErr) {
		t.Error("init registered the catalog despite a missing required --workspace-dirs-root answer")
	}
}

func TestInit_NonInteractiveWithoutYesRefuses(t *testing.T) {
	resetInitFlags(t)
	setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "okf")

	// execRoot's stdin is a bytes.Buffer, never a terminal, and --yes is
	// absent: init must refuse rather than silently proceeding or hanging
	// on a prompt no one will answer.
	out, err := execRoot(t, "init",
		"--catalog-alias", "local",
		"--catalog-source", "path+editable://"+fixture,
		"--enable", "okf",
	)
	if err == nil {
		t.Fatalf("Execute() succeeded without --yes in a non-interactive run; output:\n%s", out)
	}
}

func TestInit_InteractivePromptsDriveTheSameFlow(t *testing.T) {
	resetInitFlags(t)
	configHome := setInitConfigHome(t)
	fixture := writeInitCatalogFixture(t, "agent/claude", "channel/slack")

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = origInteractive })

	workspaceDirs := filepath.Join(t.TempDir(), "work")
	stdin := strings.Join([]string{
		"local",                      // catalog alias
		"path+editable://" + fixture, // catalog source
		"",                           // catalog subdir (blank: source root)
		"",                           // catalog revision (blank: path source)
		"1, channel/slack",           // plugin selection (mixed index + path)
		"^acme/",                     // resource allowlist
		workspaceDirs,                // workspaceDirs root
		"y",                          // final confirmation
	}, "\n") + "\n"

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs([]string{"init"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out.String())
	}

	catalogsData, readErr := os.ReadFile(filepath.Join(configHome, "catalogs.toml"))
	if readErr != nil {
		t.Fatalf("catalogs.toml not written: %v", readErr)
	}
	if !strings.Contains(string(catalogsData), "agent/claude") || !strings.Contains(string(catalogsData), "channel/slack") {
		t.Errorf("catalogs.toml missing enabled plugins:\n%s", catalogsData)
	}
	configData, readErr := os.ReadFile(filepath.Join(configHome, "config.toml"))
	if readErr != nil {
		t.Fatalf("config.toml not written: %v", readErr)
	}
	if !strings.Contains(string(configData), workspaceDirs) || !strings.Contains(string(configData), "^acme/") {
		t.Errorf("config.toml missing prompted answers:\n%s", configData)
	}
}
