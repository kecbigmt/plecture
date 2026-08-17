package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/confighome"
)

// Callers that pass --config-home are responsible for resetting
// configHomeFlag afterward: cobra flag bindings outlive a single Execute()
// call, unlike t.Setenv-backed env vars, which restore themselves.
func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestConfigShow_DefaultsToXDGConfigPlect(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(confighome.EnvVar, "")
	t.Setenv(confighome.XDGEnvVar, "")

	out, err := execRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	want := filepath.Join(fakeHome, ".config", "plect")
	if !strings.Contains(out, want) {
		t.Errorf("output = %q, want to contain %q", out, want)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("output = %q, want to report the default source", out)
	}
}

func TestConfigShow_EnvVarOverridesDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(confighome.XDGEnvVar, "")
	envDir := t.TempDir()
	t.Setenv(confighome.EnvVar, envDir)

	out, err := execRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, envDir) {
		t.Errorf("output = %q, want to contain %q", out, envDir)
	}
	if !strings.Contains(out, confighome.EnvVar) {
		t.Errorf("output = %q, want to name %s as the source", out, confighome.EnvVar)
	}
}

func TestConfigShow_XDGConfigHomeOverridesDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(confighome.EnvVar, "")
	xdgConfigHome := t.TempDir()
	t.Setenv(confighome.XDGEnvVar, xdgConfigHome)

	out, err := execRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	want := filepath.Join(xdgConfigHome, "plect")
	if !strings.Contains(out, want) {
		t.Errorf("output = %q, want to contain %q", out, want)
	}
	if !strings.Contains(out, confighome.XDGEnvVar) {
		t.Errorf("output = %q, want to name %s as the source", out, confighome.XDGEnvVar)
	}
}

func TestConfigShow_EnvVarWinsOverXDGConfigHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(confighome.XDGEnvVar, t.TempDir())
	envDir := t.TempDir()
	t.Setenv(confighome.EnvVar, envDir)

	out, err := execRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, envDir) {
		t.Errorf("output = %q, want to contain %q", out, envDir)
	}
	if !strings.Contains(out, confighome.EnvVar) {
		t.Errorf("output = %q, want to name %s as the source", out, confighome.EnvVar)
	}
}

func TestConfigShow_FlagWinsOverEnvVar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(confighome.XDGEnvVar, "")
	envDir := t.TempDir()
	flagDir := t.TempDir()
	t.Setenv(confighome.EnvVar, envDir)
	t.Cleanup(func() { configHomeFlag = "" })

	out, err := execRoot(t, "--config-home", flagDir, "config", "show")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, flagDir) {
		t.Errorf("output = %q, want to contain flag dir %q", out, flagDir)
	}
	if strings.Contains(out, envDir) {
		t.Errorf("output = %q, should not contain shadowed env dir %q", out, envDir)
	}
	if !strings.Contains(out, "--config-home") {
		t.Errorf("output = %q, want to name --config-home as the source", out)
	}
}

// TestConfigHomeOverride_IsolatesFromRealHome is the end-to-end isolation
// test the design calls for: a populated "real" home's catalogs.toml must
// never be read once PLECT_CONFIG_HOME points elsewhere.
func TestConfigHomeOverride_IsolatesFromRealHome(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	realConfigDir := filepath.Join(fakeHome, ".config", "plect")
	if err := os.MkdirAll(realConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realCatalogs := `schema_version = 1

[[catalogs]]
alias = "home-only"
source = "path:///nonexistent-home-catalog"
plugins = []
`
	if err := os.WriteFile(filepath.Join(realConfigDir, "catalogs.toml"), []byte(realCatalogs), 0o644); err != nil {
		t.Fatal(err)
	}
	realConfigInfo, err := os.Stat(filepath.Join(realConfigDir, "catalogs.toml"))
	if err != nil {
		t.Fatal(err)
	}

	overrideDir := t.TempDir()
	overrideCatalogs := `schema_version = 1

[[catalogs]]
alias = "override-only"
source = "path:///nonexistent-override-catalog"
plugins = []
`
	if err := os.WriteFile(filepath.Join(overrideDir, "catalogs.toml"), []byte(overrideCatalogs), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(confighome.EnvVar, overrideDir)

	out, err := execRoot(t, "catalog", "list")
	if err != nil {
		t.Fatalf("Execute() error = %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "override-only") {
		t.Errorf("output missing override catalog; got:\n%s", out)
	}
	if strings.Contains(out, "home-only") {
		t.Errorf("output leaked the real-home catalog; got:\n%s", out)
	}

	// The real config directory must be untouched, not merely unread.
	after, err := os.Stat(filepath.Join(realConfigDir, "catalogs.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if after.ModTime() != realConfigInfo.ModTime() || after.Size() != realConfigInfo.Size() {
		t.Errorf("real-home catalogs.toml was modified by a run scoped to PLECT_CONFIG_HOME")
	}
}
