package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestWriteInitConfig_WritesOnlyAnsweredFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := WriteInitConfig(path, InitConfigValues{
		WorkspaceDirsRoot: "/home/user/workdirs",
		ResourceAllowlist: []string{"^acme/"},
	}); err != nil {
		t.Fatalf("WriteInitConfig: unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "schema_version = 2") {
		t.Errorf("config.toml missing schema_version:\n%s", got)
	}
	if !strings.Contains(got, `workspace_dirs_root = "/home/user/workdirs"`) {
		t.Errorf("config.toml missing workspace_dirs_root:\n%s", got)
	}
	if !strings.Contains(got, "^acme/") {
		t.Errorf("config.toml missing resource_allowlist entry:\n%s", got)
	}
	if strings.Contains(got, "detached") {
		t.Errorf("config.toml should not write fields init never asked about:\n%s", got)
	}
}

func TestWriteInitConfig_OmitsEmptyAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := WriteInitConfig(path, InitConfigValues{WorkspaceDirsRoot: "/home/user/workdirs"}); err != nil {
		t.Fatalf("WriteInitConfig: unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "resource_allowlist") {
		t.Errorf("config.toml should omit resource_allowlist when init wasn't given one:\n%s", data)
	}
}

func TestWriteInitConfig_RefusesToOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("workspace_dirs_root = \"/existing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := WriteInitConfig(path, InitConfigValues{WorkspaceDirsRoot: "/new"})
	if err == nil {
		t.Fatal("want error overwriting an existing config.toml, got nil")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "/existing") {
		t.Errorf("existing config.toml was clobbered: %s", data)
	}
}

func TestInitAlreadyDone_FalseOnFreshConfigHome(t *testing.T) {
	dir := t.TempDir()
	paths := PluginPaths{CatalogsPath: filepath.Join(dir, "catalogs.toml"), LockfilePath: filepath.Join(dir, "plect.lock"), CacheRoot: filepath.Join(dir, "cache")}

	done, err := InitAlreadyDone(filepath.Join(dir, "config.toml"), paths)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Error("InitAlreadyDone = true on a fresh config home, want false")
	}
}

func TestInitAlreadyDone_TrueWhenConfigTomlExists(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("workspace_dirs_root = \"/x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := PluginPaths{CatalogsPath: filepath.Join(dir, "catalogs.toml"), LockfilePath: filepath.Join(dir, "plect.lock"), CacheRoot: filepath.Join(dir, "cache")}

	done, err := InitAlreadyDone(configPath, paths)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Error("InitAlreadyDone = false with an existing config.toml, want true")
	}
}

func TestInitAlreadyDone_TrueWhenCatalogAlreadyRegistered(t *testing.T) {
	dir := t.TempDir()
	paths := PluginPaths{CatalogsPath: filepath.Join(dir, "catalogs.toml"), LockfilePath: filepath.Join(dir, "plect.lock"), CacheRoot: filepath.Join(dir, "cache")}
	registrations := &plugins.CatalogRegistrations{
		SchemaVersion: plugins.CatalogsSchemaVersion,
		Catalogs:      []plugins.CatalogEntry{{Alias: "local", Source: "path:///nonexistent", Plugins: []string{}}},
	}
	if err := plugins.SaveCatalogRegistrations(paths.CatalogsPath, registrations); err != nil {
		t.Fatal(err)
	}

	done, err := InitAlreadyDone(filepath.Join(dir, "config.toml"), paths)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Error("InitAlreadyDone = false with a catalog already registered, want true")
	}
}
