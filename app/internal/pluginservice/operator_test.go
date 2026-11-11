package pluginservice

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestReadEnvFileRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"duplicate key", "TOKEN=one\nTOKEN=two\n", "duplicate key TOKEN"},
		{"runtime data dir", "PLECT_DATA_DIR=/other\n", "conflicts with plect runtime"},
		{"runtime cache dir", "PLECT_CACHE_DIR=/other\n", "conflicts with plect runtime"},
		{"runtime home", "PLECT_DATA_HOME=/other\n", "conflicts with plect runtime"},
		{"braced interpolation", "TOKEN=${SECRET}\n", "invalid entry"},
		{"command interpolation", "TOKEN=$(secret)\n", "invalid entry"},
		{"malformed line", "TOKEN\n", "invalid entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "service.env")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readEnvFile(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("readEnvFile error = %v, want %q", err, tt.want)
			}
		})
	}
	if _, err := readEnvFile("relative.env"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path error = %v", err)
	}
}

func TestOperatorDeclarationsRejectInvalidServiceIdentity(t *testing.T) {
	tests := []struct {
		name, declarations, want string
	}{
		{"invalid name", `[[services]]
name = "Invalid"
executable = "local/work/worker"
env_file = %q
`, "invalid service name"},
		{"duplicate name", `[[services]]
name = "worker"
executable = "local/work/worker"
env_file = %q
[[services]]
name = "worker"
executable = "local/work/other"
env_file = %q
`, "duplicate service name"},
		{"duplicate executable", `[[services]]
name = "worker"
executable = "local/work/worker"
env_file = %q
[[services]]
name = "second"
executable = "local/work/worker"
env_file = %q
`, "duplicate executable"},
		{"unmounted", `[[services]]
name = "worker"
executable = "local/missing/worker"
env_file = %q
`, "not mounted"},
		{"not a service", `[[services]]
name = "worker"
executable = "local/work/helper"
env_file = %q
`, "not marked service = true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PLECT_CONFIG_HOME", home)
			envPath := filepath.Join(home, "service.env")
			if err := os.WriteFile(envPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(home, "services"), 0o700); err != nil {
				t.Fatal(err)
			}
			declarations := strings.ReplaceAll(tt.declarations, "%q", strconv.Quote(envPath))
			if err := os.WriteFile(filepath.Join(home, "services", "service.toml"), []byte(declarations), 0o600); err != nil {
				t.Fatal(err)
			}
			mounted := []plugins.Mounted{{ID: "local/work", Dir: home, Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "worker", Path: "worker", Service: true, ConfigSchema: map[string]any{"type": "object"}}, {Name: "helper", Path: "helper"}, {Name: "other", Path: "other", Service: true, ConfigSchema: map[string]any{"type": "object"}}}}}}
			if _, err := BuildDeclarations(mounted, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("BuildDeclarations error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestOperatorDeclarationsValidateConfigAndPrivateEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLECT_CONFIG_HOME", home)
	envPath := filepath.Join(home, "worker.env")
	if err := os.WriteFile(envPath, []byte("API_TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(home, "services", "worker.toml")
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o700); err != nil {
		t.Fatal(err)
	}
	mounted := []plugins.Mounted{{ID: "official/work", Dir: home, Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "worker", Path: "bin/worker", Service: true, ConfigSchema: map[string]any{"type": "object", "required": []any{"region"}, "properties": map[string]any{"region": map[string]any{"type": "string"}}}}}}}}
	write := func(config string) {
		t.Helper()
		content := "[[services]]\nname = \"worker\"\nexecutable = \"official/work/worker\"\nenv_file = \"" + envPath + "\"\n" + config
		if err := os.WriteFile(servicePath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[services.config]\nregion = 42\n")
	if _, err := BuildDeclarations(mounted, nil); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("schema error = %v", err)
	}
	write("[services.config]\nregion = \"west\"\n")
	decls, err := BuildDeclarations(mounted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 1 || decls[0].Env["API_TOKEN"] != "secret" || decls[0].Config["region"] != "west" {
		t.Fatalf("declarations = %+v", decls)
	}
	if err := os.Chmod(envPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDeclarations(mounted, nil); err == nil || !strings.Contains(err.Error(), "env_file") {
		t.Fatalf("env_file error = %v", err)
	}
}
