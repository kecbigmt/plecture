package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalogRegistrations_MissingFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalogs.toml")

	r, err := LoadCatalogRegistrations(path)
	if err != nil {
		t.Fatalf("LoadCatalogRegistrations: unexpected error: %v", err)
	}
	if len(r.Catalogs) != 0 {
		t.Fatalf("Catalogs = %+v, want empty", r.Catalogs)
	}
}

func TestLoadCatalogRegistrations_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalogs.toml")
	content := `
schema_version = 1

[[catalogs]]
alias = "official"
source = "git+https://github.com/example/plect-plugins"
plugins = ["github", "agent/codex-tasks"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := LoadCatalogRegistrations(path)
	if err != nil {
		t.Fatalf("LoadCatalogRegistrations: unexpected error: %v", err)
	}
	if len(r.Catalogs) != 1 || r.Catalogs[0].Alias != "official" || len(r.Catalogs[0].Plugins) != 2 {
		t.Fatalf("Catalogs = %+v", r.Catalogs)
	}
}

func TestLoadCatalogRegistrations_UnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalogs.toml")
	if err := os.WriteFile(path, []byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalogRegistrations(path); err == nil {
		t.Fatal("want error for an unknown schema_version, got nil")
	}
}

func TestLoadCatalogRegistrations_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalogs.toml")
	if err := os.WriteFile(path, []byte("not valid toml [[["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalogRegistrations(path); err == nil {
		t.Fatal("want error for malformed catalogs.toml, got nil")
	}
}

func TestSaveCatalogRegistrations_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "catalogs.toml")
	r := &CatalogRegistrations{
		SchemaVersion: CatalogsSchemaVersion,
		Catalogs: []CatalogEntry{{
			Alias:   "official",
			Source:  "git+https://github.com/example/plect-plugins",
			Plugins: []string{"github"},
		}},
	}

	if err := SaveCatalogRegistrations(path, r); err != nil {
		t.Fatalf("SaveCatalogRegistrations: unexpected error: %v", err)
	}

	got, err := LoadCatalogRegistrations(path)
	if err != nil {
		t.Fatalf("LoadCatalogRegistrations: unexpected error: %v", err)
	}
	if len(got.Catalogs) != 1 || got.Catalogs[0].Alias != "official" || got.Catalogs[0].Plugins[0] != "github" {
		t.Fatalf("round-tripped Catalogs = %+v", got.Catalogs)
	}
}

func TestCatalogRegistrations_Find(t *testing.T) {
	r := &CatalogRegistrations{Catalogs: []CatalogEntry{{Alias: "official"}, {Alias: "team"}}}

	if _, ok := r.Find("official"); !ok {
		t.Error("Find(\"official\") = not found, want found")
	}
	if _, ok := r.Find("missing"); ok {
		t.Error("Find(\"missing\") = found, want not found")
	}
}

func TestCatalogRegistrations_SplitPluginID(t *testing.T) {
	r := &CatalogRegistrations{Catalogs: []CatalogEntry{
		{Alias: "official", Source: "git+https://x", Plugins: []string{"github", "agent/codex-tasks"}},
	}}

	entry, path, err := r.SplitPluginID("official/agent/codex-tasks")
	if err != nil {
		t.Fatalf("SplitPluginID: unexpected error: %v", err)
	}
	if entry.Alias != "official" || path != "agent/codex-tasks" {
		t.Errorf("got entry=%+v path=%q", entry, path)
	}
}

func TestCatalogRegistrations_SplitPluginID_UnregisteredAlias(t *testing.T) {
	r := &CatalogRegistrations{}
	if _, _, err := r.SplitPluginID("official/github"); err == nil {
		t.Fatal("want error for an unregistered catalog alias, got nil")
	}
}

func TestCatalogRegistrations_SplitPluginID_PathNotEnabled(t *testing.T) {
	r := &CatalogRegistrations{Catalogs: []CatalogEntry{{Alias: "official", Plugins: []string{"github"}}}}
	if _, _, err := r.SplitPluginID("official/agent/codex-tasks"); err == nil {
		t.Fatal("want error for a path not enabled from the catalog, got nil")
	}
}

func TestCatalogRegistrations_SplitPluginID_MalformedID(t *testing.T) {
	r := &CatalogRegistrations{}
	for _, id := range []string{"noslash", "/leading-empty-alias", "trailing-empty-path/"} {
		if _, _, err := r.SplitPluginID(id); err == nil {
			t.Errorf("SplitPluginID(%q): want error, got nil", id)
		}
	}
}
