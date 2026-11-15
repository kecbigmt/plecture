package effect

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestPluginExecutableReceivesSharedCacheAndDeclaredServiceEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLECT_CONFIG_HOME", home)
	t.Setenv("PLECT_CACHE_HOME", filepath.Join(home, "cache"))
	envFile := filepath.Join(home, "svc.env")
	if err := os.WriteFile(envFile, []byte("TOKEN=private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "services"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "services", "svc.toml"), []byte("[[services]]\nname = \"svc\"\nexecutable = \"local/work/worker\"\nenv_file = \""+envFile+"\"\n[services.config]\nregion = \"west\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "services", "invalid.toml"), []byte("[[services]]\nname = \"invalid\"\nexecutable = \"other/missing\"\nenv_file = \"relative.env\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounted := []plugins.Mounted{{ID: "local/work", Dir: home, Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "worker", Path: "bin/worker", Service: true, ConfigSchema: map[string]any{"type": "object"}}, {Name: "observe", Path: "bin/observe"}}}}}
	worker, err := pluginEnvironment(filepath.Join(home, "bin", "worker"), mounted)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TOKEN=private", "PLECT_CACHE_DIR=" + filepath.Join(home, "cache", "plugins", "local", "work")} {
		if !slices.Contains(worker, want) {
			t.Fatalf("worker env = %v", worker)
		}
	}
	observe, err := pluginEnvironment(filepath.Join(home, "bin", "observe"), mounted)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(observe, func(s string) bool { return strings.HasPrefix(s, "TOKEN=") }) {
		t.Fatalf("observe env = %v", observe)
	}
	if !slices.Contains(observe, "PLECT_CACHE_DIR="+filepath.Join(home, "cache", "plugins", "local", "work")) {
		t.Fatalf("observe env = %v", observe)
	}
	action, err := lang.ParseAction(map[string]any{"type": "exec", "bin": "local/work/worker", "args": []any{map[string]any{"from": "service.config.region"}}}, lang.Position{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := HookEval(lang.Roots{}, mounted, "", lang.Ownership{}).Exec(action)
	if err != nil {
		t.Fatal(err)
	}
	private, err := resolved.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Argv) != 2 || resolved.Argv[1] != "west" || !slices.Contains(private, "TOKEN=private") {
		t.Fatalf("execution = %+v", resolved)
	}
	if err := os.Chmod(envFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginEnvironment(filepath.Join(home, "bin", "worker"), mounted); err == nil || !strings.Contains(err.Error(), "env_file") {
		t.Fatalf("invalid worker declaration error = %v", err)
	}
	if err := os.Chmod(envFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "services", "svc.toml")); err != nil {
		t.Fatal(err)
	}
	plain, err := pluginEnvironment(filepath.Join(home, "bin", "worker"), mounted)
	if err != nil || !slices.Contains(plain, "PLECT_CACHE_DIR="+filepath.Join(home, "cache", "plugins", "local", "work")) {
		t.Fatalf("undeclared service helper environment = %v, %v", plain, err)
	}
	if slices.ContainsFunc(plain, func(s string) bool { return strings.HasPrefix(s, "TOKEN=") }) {
		t.Fatalf("undeclared service helper received private env: %v", plain)
	}
	if _, err := HookEval(lang.Roots{}, mounted, "", lang.Ownership{}).Exec(action); err == nil || !strings.Contains(err.Error(), "service.config") {
		t.Fatalf("undeclared service config error = %v", err)
	}
}
