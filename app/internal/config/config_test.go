package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Detached != true {
		t.Errorf("Detached = %v, want true", cfg.Detached)
	}
	if cfg.RepoAllowlist != nil {
		t.Errorf("RepoAllowlist = %v, want nil", cfg.RepoAllowlist)
	}
}

func TestIsRepoAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		repo      string
		want      bool
	}{
		{
			name:      "empty allowlist allows all",
			allowlist: nil,
			repo:      "any/repo",
			want:      true,
		},
		{
			name:      "repo in allowlist",
			allowlist: []string{"org/repo-a", "org/repo-b"},
			repo:      "org/repo-a",
			want:      true,
		},
		{
			name:      "repo not in allowlist",
			allowlist: []string{"org/repo-a"},
			repo:      "org/repo-b",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{RepoAllowlist: tt.allowlist}
			got := cfg.IsRepoAllowed(tt.repo)
			if got != tt.want {
				t.Errorf("IsRepoAllowed(%q) = %v, want %v", tt.repo, got, tt.want)
			}
		})
	}
}

func TestLoad_WithConfigFile(t *testing.T) {
	// Create a temp home directory with a config file
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	configDir := filepath.Join(tmpHome, ".config", "sennit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configContent := `
worktrees_root = "~/my-worktrees"
repo_allowlist = ["org/repo-a", "org/repo-b"]
detached = false
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if cfg.WorktreesRoot != filepath.Join(tmpHome, "my-worktrees") {
		t.Errorf("WorktreesRoot = %q, want %q", cfg.WorktreesRoot, filepath.Join(tmpHome, "my-worktrees"))
	}
	if len(cfg.RepoAllowlist) != 2 {
		t.Errorf("RepoAllowlist length = %d, want 2", len(cfg.RepoAllowlist))
	}
	if cfg.Detached != false {
		t.Errorf("Detached = %v, want false", cfg.Detached)
	}
}

func TestLoad_NoConfigFile(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg := Load()

	// Should return defaults
	if cfg.Detached != true {
		t.Errorf("Detached = %v, want true", cfg.Detached)
	}
}

func TestLoad_PopulatesBaseDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	configDir := filepath.Join(tmpHome, ".config", "sennit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("inputs_schema_file = \"in.schema.json\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if cfg.BaseDir != configDir {
		t.Errorf("BaseDir = %q, want %q", cfg.BaseDir, configDir)
	}
	want := filepath.Join(configDir, "in.schema.json")
	if got := cfg.ResolvedInputsSchemaPath(); got != want {
		t.Errorf("ResolvedInputsSchemaPath = %q, want %q", got, want)
	}
}

func TestLoad_InlineInputsSchema(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	configDir := filepath.Join(tmpHome, ".config", "sennit")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgContent := `
[inputs_schema]
type = "object"
required = ["template"]

[inputs_schema.properties]
template = { type = "string" }
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if cfg.InputsSchema == nil {
		t.Fatal("expected InputsSchema parsed from TOML")
	}
	if cfg.InputsSchema["type"] != "object" {
		t.Errorf("InputsSchema[type] = %v, want object", cfg.InputsSchema["type"])
	}
}

func TestResolvedInputsSchemaPath(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty", Config{}, ""},
		{"absolute path", Config{InputsSchemaFile: "/abs/in.json"}, "/abs/in.json"},
		{"relative with base", Config{InputsSchemaFile: "in.json", BaseDir: "/cfg"}, "/cfg/in.json"},
		{"relative without base returns as-is", Config{InputsSchemaFile: "in.json"}, "in.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.ResolvedInputsSchemaPath(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsResourceAllowed(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		resource string
		want     bool
		wantErr  bool
	}{
		{name: "both lists empty allows all", cfg: Config{}, resource: "anything", want: true},
		{
			name:     "resource pattern matches",
			cfg:      Config{ResourceAllowlist: []string{`^https://github\.com/org/`}},
			resource: "https://github.com/org/repo/issues/1",
			want:     true,
		},
		{
			name:     "resource pattern rejects",
			cfg:      Config{ResourceAllowlist: []string{`^https://github\.com/org/`}},
			resource: "https://github.com/evil/repo/issues/1",
			want:     false,
		},
		{
			name:     "legacy repo_allowlist gates github urls",
			cfg:      Config{RepoAllowlist: []string{"org/repo"}},
			resource: "https://github.com/org/repo/pull/3",
			want:     true,
		},
		{
			name:     "legacy repo_allowlist rejects other repos",
			cfg:      Config{RepoAllowlist: []string{"org/repo"}},
			resource: "https://github.com/org/other/pull/3",
			want:     false,
		},
		{
			name:     "legacy repo_allowlist rejects non-url resources",
			cfg:      Config{RepoAllowlist: []string{"org/repo"}},
			resource: "jira-PROJ-1",
			want:     false,
		},
		{
			name:     "non-github resource allowed via resource pattern",
			cfg:      Config{ResourceAllowlist: []string{`^https://jira\.example\.com/browse/`}},
			resource: "https://jira.example.com/browse/PROJ-1",
			want:     true,
		},
		{
			name:    "invalid pattern surfaces error",
			cfg:     Config{ResourceAllowlist: []string{`([`}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.IsResourceAllowed(tt.resource)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSessionNameAllowed(t *testing.T) {
	tests := []struct {
		name    string
		guard   string
		session string
		want    bool
		wantErr bool
	}{
		{name: "empty guard allows all", guard: "", session: "exampleorg/x-26", want: true},
		{name: "owner-prefix guard matches own owner", guard: "^acme/", session: "acme/widgets-1", want: true},
		{name: "owner-prefix guard rejects other owner", guard: "^acme/", session: "exampleorg/x-26", want: false},
		{name: "tagged session still matches", guard: "^acme/", session: "acme/widgets-1+review", want: true},
		{name: "trailing slash blocks lookalike owner", guard: "^acme/", session: "acme-evil/x-1", want: false},
		{name: "invalid pattern fails closed", guard: "([", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{SessionGuard: tt.guard}
			got, err := cfg.IsSessionNameAllowed(tt.session)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("IsSessionNameAllowed(%q) with guard %q = %v, want %v", tt.session, tt.guard, got, tt.want)
			}
		})
	}
}
