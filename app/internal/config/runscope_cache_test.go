package config

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func sessionWithCapturedRevision(t *testing.T, cfg *Config, workspace string) *domain.Session {
	t.Helper()
	revision, err := cfg.CaptureWorkflowRevision(filepath.Join(workspace, "cwd"), "default")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	return &domain.Session{Workflow: "default", WorkflowRevision: digest, WorkflowRevisionBody: string(body)}
}

func TestCurrentPlanRunScopedNodeSet_CachesPerConfigLifetime(t *testing.T) {
	base := writeRunScopedWorkflow(t)
	cfg := &Config{BaseDir: base}
	s := sessionWithCapturedRevision(t, cfg, "")

	first, ok := cfg.CurrentPlanRunScopedNodeSet(s)
	if !ok || len(first) != 2 {
		t.Fatalf("first call = %+v, ok=%v, want the two-node fixture set", first, ok)
	}

	writeFile(t, filepath.Join(base, "workflows", "default.toml"), `
[default]
kind = "workflow"
resource = "issue"

[[default.nodes]]
id   = "pane"
uses = "pane"

[[default.nodes]]
id   = "agent"
uses = "agent"

[[default.nodes]]
id   = "extra"
uses = "agent"
`)

	again, ok := cfg.CurrentPlanRunScopedNodeSet(s)
	if !ok || len(again) != 2 {
		t.Errorf("second call on the same *Config = %+v, ok=%v, want the cached two-node set unchanged by the on-disk edit", again, ok)
	}
}

func TestCurrentPlanRunScopedNodeSet_NewConfigSeesEdit(t *testing.T) {
	base := writeRunScopedWorkflow(t)
	initial := &Config{BaseDir: base}
	s := sessionWithCapturedRevision(t, initial, "")
	if _, ok := initial.CurrentPlanRunScopedNodeSet(s); !ok {
		t.Fatal("priming call: ok = false")
	}

	writeFile(t, filepath.Join(base, "workflows", "default.toml"), `
[default]
kind = "workflow"
resource = "issue"

[[default.nodes]]
id   = "pane"
uses = "pane"

[[default.nodes]]
id   = "agent"
uses = "agent"

[[default.nodes]]
id   = "extra"
uses = "agent"
`)

	freshConfig := &Config{BaseDir: base}
	newSession := sessionWithCapturedRevision(t, freshConfig, "")
	fresh, ok := freshConfig.CurrentPlanRunScopedNodeSet(newSession)
	if !ok || len(fresh) != 3 {
		t.Errorf("call on a fresh *Config = %+v, ok=%v, want the edited three-node set", fresh, ok)
	}
}
