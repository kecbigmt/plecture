package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTaskFile writes a single task definition file directly (not via the
// taskFixture helper, which lives in the service package): the config
// package's own tests write `tasks/<id>.toml` under a base dir.
func writeTaskFile(t *testing.T, base, id, body string) {
	t.Helper()
	dir := filepath.Join(base, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A surviving chains/*.toml file gets one warning naming it, so a migration

// A definition root admits no non-definition TOML, so a leftover chains/ file
// from before chains moved into the declaration that fires them stops the
// layer from loading. The error names the file and the fix, because the
// generic "not a definition table" diagnostic explains neither.
func TestDiscoverLayer_RetiredChainsDirIsAnActionableLoadError(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "chains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte("[[work.chains]]\nid=\"review\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
	if err == nil {
		t.Fatal("expected a load error for a leftover chains/*.toml")
	}
	for _, want := range []string{"review.toml", "[[chains]]", "delete the retired chains/ directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// No chains/ dir at all is the common case and must load.
func TestDiscoverLayer_NoChainsDirLoads(t *testing.T) {
	if _, err := (&Config{BaseDir: t.TempDir()}).LoadTaskDefinitions(""); err != nil {
		t.Fatalf("LoadTaskDefinitions: %v", err)
	}
}

// effectPreamble is the definition table every chain row declares its
// `[[work.chains]]` under.
const effectPreamble = `
[work]
kind  = "effect"
scope = "run"

[work.setup]
type   = "shell"
script = "true"

`
