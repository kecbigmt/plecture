package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func decodeTrust(t *testing.T, path string) *Config {
	t.Helper()
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return &c
}

func TestAddTrustedProjectRoot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing string
		want     []string
		// kept are byte sequences of the original file that must survive.
		kept []string
	}{
		{
			name: "a missing config.toml is created with the root",
			want: []string{"/projects/widgets"},
		},
		{
			name: "an absent key is added at the top level, ahead of any table",
			existing: `# machine settings
schema_version = 2 # the dialect

[inputs_schema]
type = "object"
`,
			want: []string{"/projects/widgets"},
			kept: []string{"# machine settings\n", "schema_version = 2 # the dialect\n", "[inputs_schema]\ntype = \"object\"\n"},
		},
		{
			name: "a multi-line list keeps its entries and gains the root",
			existing: `schema_version = 2
trusted_project_roots = [
  "/projects/gadgets",
]
# after the list
plugin_dirs = ["/opt/plugins"]
`,
			want: []string{"/projects/gadgets", "/projects/widgets"},
			kept: []string{"schema_version = 2\n", "\n# after the list\nplugin_dirs = [\"/opt/plugins\"]\n"},
		},
		{
			name:     "a final schema_version line without a newline is not joined to the key",
			existing: "schema_version = 2",
			want:     []string{"/projects/widgets"},
		},
		{
			name:     "a root already listed leaves the file unchanged",
			existing: "schema_version = 2\ntrusted_project_roots = [\"/projects/widgets\"] # trusted\n",
			want:     []string{"/projects/widgets"},
			kept:     []string{"schema_version = 2\ntrusted_project_roots = [\"/projects/widgets\"] # trusted\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if tc.existing != "" {
				writeFile(t, path, tc.existing)
			}

			if err := AddTrustedProjectRoot(path, "/projects/widgets"); err != nil {
				t.Fatalf("AddTrustedProjectRoot: %v", err)
			}

			got := decodeTrust(t, path)
			if !slices.Equal(got.TrustedProjectRoots, tc.want) {
				t.Errorf("trusted_project_roots = %v, want %v", got.TrustedProjectRoots, tc.want)
			}
			if got.SchemaVersion == 0 {
				t.Error("schema_version is missing")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, kept := range tc.kept {
				if !strings.Contains(string(body), kept) {
					t.Errorf("%q was not preserved in:\n%s", kept, body)
				}
			}
		})
	}
}

func TestAddTrustedProjectRoot_KeepsTablesInTheirTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "schema_version = 2\n\n[inputs_schema]\ntype = \"object\"\n")

	if err := AddTrustedProjectRoot(path, "/projects/widgets"); err != nil {
		t.Fatalf("AddTrustedProjectRoot: %v", err)
	}

	got := decodeTrust(t, path)
	if got.InputsSchema["type"] != "object" || len(got.InputsSchema) != 1 {
		t.Errorf("inputs_schema = %v, want the table untouched by the new key", got.InputsSchema)
	}
}

func TestAddTrustedProjectRoot_WritesThroughASymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "managed", "config.toml")
	writeFile(t, target, "schema_version = 2\n")
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := AddTrustedProjectRoot(link, "/projects/widgets"); err != nil {
		t.Fatalf("AddTrustedProjectRoot: %v", err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink: %v, %v", info, err)
	}
	if got := decodeTrust(t, target).TrustedProjectRoots; !slices.Equal(got, []string{"/projects/widgets"}) {
		t.Errorf("target trusted_project_roots = %v, want the root", got)
	}
}
