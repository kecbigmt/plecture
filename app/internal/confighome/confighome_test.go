package confighome

import (
	"path/filepath"
	"testing"
)

func TestResolve_DefaultsToXDGConfigPlect(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv(EnvVar, "")

	want := filepath.Join(tmpHome, ".config", "plect")
	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolve_EnvVarOverridesDefault(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	override := filepath.Join(t.TempDir(), "custom-config")
	t.Setenv(EnvVar, override)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != override {
		t.Fatalf("Resolve() = %q, want %q", got, override)
	}
}
