package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/plecture/plect/app/internal/config"
)

func writeResourceDefFixture(t *testing.T, baseDir, id, toml string) {
	t.Helper()
	dir := filepath.Join(baseDir, "resources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResourceStatus_ObservesMatchingDefinition(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeResourceDefFixture(t, cfg.BaseDir, "github", `
match   = '^https://github\.com/'
observe = "echo '{\"resource_kind\":\"pull\",\"checks_status\":\"SUCCESS\"}'"
`)

	result, err := ResourceStatus(cfg, ResourceStatusParams{ResourceID: "https://github.com/o/r/pull/5"})
	if err != nil {
		t.Fatalf("ResourceStatus: %v", err)
	}
	if result.Definition != "github" {
		t.Errorf("Definition = %q, want github", result.Definition)
	}
	if result.State["resource_kind"] != "pull" || result.State["checks_status"] != "SUCCESS" {
		t.Errorf("State = %v, want the observed fields", result.State)
	}
}

func TestResourceStatus_NoMatchingDefinitionIsError(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	if _, err := ResourceStatus(cfg, ResourceStatusParams{ResourceID: "local-okf://kec/goals/x.md"}); err == nil {
		t.Fatal("expected an error when no resource definition recognizes the id")
	}
}

func TestResourceStatus_EmptyResourceIDIsError(t *testing.T) {
	cfg := &config.Config{}
	if _, err := ResourceStatus(cfg, ResourceStatusParams{ResourceID: "  "}); err == nil {
		t.Fatal("expected an error for a blank resource id")
	}
}
