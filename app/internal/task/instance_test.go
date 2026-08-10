package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

func TestInstanceKey(t *testing.T) {
	if got := InstanceKey("review", ""); got != "review" {
		t.Errorf("blank instance id: got %q", got)
	}
	if got := InstanceKey("review", "3"); got != "review#3" {
		t.Errorf("with instance id: got %q", got)
	}
}

func TestNextInstanceNumber(t *testing.T) {
	produced := func() *contract.TaskState {
		return &contract.TaskState{Status: contract.TaskStatusProduced, Dynamic: true}
	}

	t.Run("empty starts at 1", func(t *testing.T) {
		if got := NextInstanceNumber("review", map[string]*contract.TaskState{}); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("increments per task, independently", func(t *testing.T) {
		tasks := map[string]*contract.TaskState{
			"review#1": produced(),
			"review#2": produced(),
			"build#1":  produced(),
		}
		if got := NextInstanceNumber("review", tasks); got != 3 {
			t.Errorf("review: got %d, want 3", got)
		}
		if got := NextInstanceNumber("build", tasks); got != 2 {
			t.Errorf("build: got %d, want 2 (independent numbering)", got)
		}
	})

	t.Run("monotonic across cleaned (no reuse)", func(t *testing.T) {
		tasks := map[string]*contract.TaskState{
			"review#1": {Status: contract.TaskStatusCleaned, Dynamic: true},
			"review#2": {Status: contract.TaskStatusCleaned, Dynamic: true},
		}
		if got := NextInstanceNumber("review", tasks); got != 3 {
			t.Errorf("got %d, want 3 (cleaned instances still occupy numbers)", got)
		}
	})

	t.Run("ignores non-numeric and prefix-only keys", func(t *testing.T) {
		tasks := map[string]*contract.TaskState{
			"review":     produced(), // static node, no suffix
			"review#abc": produced(), // non-numeric suffix
			"reviewer#9": produced(), // different task (no '#': "reviewer" != prefix "review#")
			"review#7":   produced(),
			"@workflow":  produced(),
		}
		if got := NextInstanceNumber("review", tasks); got != 8 {
			t.Errorf("got %d, want 8", got)
		}
	})
}

// resolveDef compiles a single definition the way the dynamic path does.
func resolveDef(t *testing.T, def config.TaskDefinition, key string) Resolved {
	t.Helper()
	r, err := ResolveDefinition(def, key)
	if err != nil {
		t.Fatalf("ResolveDefinition: %v", err)
	}
	return r
}

func TestExecuteTaskSetup_ParsesOutputs(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	def := config.TaskDefinition{ID: "review", Scope: "session", Setup: `echo '{"ready":"yes"}'`}
	r := resolveDef(t, def, "review#1")

	outputs, _, err := ExecuteTaskSetup(r, nil, SessionVars{Name: "x"}, map[string]*contract.TaskState{})
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if outputs["ready"] != "yes" {
		t.Errorf("outputs = %v", outputs)
	}
}

func TestExecuteTaskSetup_InputSchemaRejection(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	def := config.TaskDefinition{
		ID:    "work",
		Scope: "session",
		Setup: `echo '{}'`,
		InputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"intent"},
			"properties": map[string]any{
				"intent": map[string]any{"type": "string"},
			},
		},
	}
	r := resolveDef(t, def, "work#1")
	// No intent bound → schema validation fails; ExecuteTaskSetup returns an
	// error and writes no state (the caller persists produced/failed).
	if _, _, err := ExecuteTaskSetup(r, map[string]any{}, SessionVars{}, nil); err == nil {
		t.Fatal("expected input schema validation error")
	}
}

// A dynamic instance's cleanup must render {{.ResourceID}} as the instance's own
// resource (persisted in state), not the session's.
func TestRunCleanup_DynamicInstanceResource(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	marker := filepath.Join(t.TempDir(), "resource")
	r := Resolved{NodeID: "review#1", Scope: "session", Cleanup: "echo {{.ResourceID}} > " + marker}
	tasks := map[string]*contract.TaskState{
		"review#1": {Scope: "session", Status: contract.TaskStatusProduced, Dynamic: true, Resource: "pr-99", Outputs: map[string]any{}},
	}
	if err := RunCleanup([]Resolved{r}, SessionVars{ResourceID: "session-res"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "pr-99" {
		t.Errorf("cleanup .ResourceID = %q, want pr-99 (instance resource, not session)", got)
	}
}

func TestExecuteTaskSetup_SeesWorkflowOutputs(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	def := config.TaskDefinition{ID: "review", Scope: "session", Setup: `printf '{"branch":"%s"}' '{{.Workflow.outputs.branch}}'`}
	r := resolveDef(t, def, "review#1")
	wfTasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {Status: contract.TaskStatusProduced, Outputs: map[string]any{"branch": "feat/x"}},
	}
	outputs, _, err := ExecuteTaskSetup(r, nil, SessionVars{}, wfTasks)
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	if outputs["branch"] != "feat/x" {
		t.Errorf("expected @workflow outputs visible, got %v", outputs)
	}
}
