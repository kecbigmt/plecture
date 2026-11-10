package pluginservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

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
