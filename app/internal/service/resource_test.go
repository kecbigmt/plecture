package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
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
[github]
kind  = "resource"
name  = "fixture"
match = '^https://github\.com/'

[github.observe]
type    = "exec"
command = "printf"
args    = ['{"resource_kind":"pull","checks_status":"SUCCESS"}']
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

func TestResourceStatus_PassesTheSuppliedInputsToObserve(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	writeResourceDefFixture(t, cfg.BaseDir, "forge", `
[forge]
kind  = "resource"
name  = "fixture"
match = '^forge://'

[forge.observe]
type    = "exec"
command = "printf"
args    = ['{"token":"%s"}', { from = "resource.inputs.token" }]

[forge.inputs_schema]
type     = "object"
required = ["token"]

[forge.inputs_schema.properties]
token = { type = "string" }
`)

	result, err := ResourceStatus(cfg, ResourceStatusParams{ResourceID: "forge://x", Inputs: map[string]any{"token": "secret"}})
	if err != nil {
		t.Fatalf("ResourceStatus: %v", err)
	}
	if result.State["token"] != "secret" {
		t.Errorf("State = %v, want observe to see resource.inputs.token", result.State)
	}

	_, err = ResourceStatus(cfg, ResourceStatusParams{ResourceID: "forge://x"})
	if code := errorCode(err); code != ErrInvalidInput {
		t.Errorf("missing required inputs: err = %v (code %q), want %q", err, code, ErrInvalidInput)
	}
}

func errorCode(err error) string {
	var svcErr *Error
	if errors.As(err, &svcErr) {
		return svcErr.Code
	}
	return ""
}
