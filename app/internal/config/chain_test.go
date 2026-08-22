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
// straggler has a signal that the rule stopped firing instead of silence.
func TestLegacyChainsDirNotice_WarnsPerFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "chains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte("[[work.chains]]\nid=\"review\"\nworkflow=\"codex\"\n[work.chains.when]\nall=[{judge_pending=\"x\"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnings, err := (&Config{BaseDir: base}).LegacyChainsDirNotice()
	if err != nil {
		t.Fatalf("LegacyChainsDirNotice: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "review.toml") {
		t.Fatalf("expected one warning naming the file, got %v", warnings)
	}
}

// No chains/ dir at all is the common case and must not warn.
func TestLegacyChainsDirNotice_NoDirIsSilent(t *testing.T) {
	warnings, err := (&Config{BaseDir: t.TempDir()}).LegacyChainsDirNotice()
	if err != nil {
		t.Fatalf("LegacyChainsDirNotice: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
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
