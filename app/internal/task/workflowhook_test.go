package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// TestRunWorkflowSetup_ResolvesBinReference pins that a workspace provider's
// setup/cleanup hooks can invoke a plugin-shipped executable through
// `{{bin ...}}`, the same helper task setup/cleanup and resource observe
// hooks already get — a workspace provider hook that only has bare command
// names on `PATH` available cannot reliably invoke its own plugin's
// executables once that plugin is mounted read-only rather than installed
// onto `PATH`.
func TestRunWorkflowSetup_ResolvesBinReference(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "github-worktree", Path: "bin/github-worktree"})}
	prov := config.WorkspaceProviderConfig{
		ID:    "wf",
		Setup: `echo "{\"workspace_dir\":\"/tmp/x\",\"bin\":\"{{bin "official/github/github-worktree"}}\"}"`,
	}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunWorkflowSetup(prov, WorkflowHookVars{ResourceID: "r", SessionName: "s", Plugins: mounted}, tasks, nil)
	if err != nil {
		t.Fatalf("RunWorkflowSetup: %v", err)
	}
	want := filepath.Join("/mnt/github", "bin", "github-worktree")
	if outputs["bin"] != want {
		t.Errorf("bin = %v, want %q", outputs["bin"], want)
	}
}

func TestRunWorkflowSetup_ProducesWorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	prov := config.WorkspaceProviderConfig{
		ID:    "wf",
		Setup: `echo "{\"workspace_dir\":\"` + dir + `\",\"branch\":\"issue/1\"}"`,
	}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunWorkflowSetup(prov, WorkflowHookVars{ResourceID: "https://example.com/1", SessionName: "s"}, tasks, nil)
	if err != nil {
		t.Fatalf("RunWorkflowSetup: %v", err)
	}
	if outputs["workspace_dir"] != dir {
		t.Errorf("workspace_dir = %v, want %s", outputs["workspace_dir"], dir)
	}
	st := tasks[contract.WorkflowPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusProduced {
		t.Fatalf("pseudo-node state = %+v, want produced", st)
	}
	if st.Scope != contract.TaskScopeSession {
		t.Errorf("scope = %q, want session", st.Scope)
	}
}

func TestRunWorkflowSetup_TemplateVars(t *testing.T) {
	prov := config.WorkspaceProviderConfig{
		ID:    "wf",
		Setup: `echo "{\"workspace_dir\":\"/tmp/x\",\"resource\":\"{{.ResourceID}}\",\"session\":\"{{.SessionName}}\",\"root\":\"{{.WorkspaceDirsRoot}}\",\"tpl\":\"{{get .SessionInputs "template"}}\"}"`,
	}
	tasks := map[string]*contract.TaskState{}
	outputs, err := RunWorkflowSetup(prov, WorkflowHookVars{
		ResourceID:        "res-1",
		SessionName:       "sess-1",
		WorkspaceDirsRoot: "/roots/workspace_dirs",
		SessionInputs:     map[string]any{"template": "review"},
	}, tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outputs["resource"] != "res-1" || outputs["session"] != "sess-1" || outputs["root"] != "/roots/workspace_dirs" || outputs["tpl"] != "review" {
		t.Errorf("template vars not rendered: %v", outputs)
	}
}

// TestRunWorkflowSetup_ShellQuoteNeutralizesMetacharacters pins the
// shellQuote template func as the fix for hook authors interpolating
// attacker-influenced values (resource ids, session names) into a command
// string that runs under bash -c: a value carrying shell metacharacters must
// reach the invoked command as one literal argument, not be re-parsed as
// shell syntax.
func TestRunWorkflowSetup_ShellQuoteNeutralizesMetacharacters(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	out := filepath.Join(dir, "out")
	prov := config.WorkspaceProviderConfig{
		ID: "wf",
		Setup: `printf '%s' {{.ResourceID | shellQuote}} > ` + out + ` && ` +
			`echo "{\"workspace_dir\":\"` + dir + `\",\"branch\":\"issue/1\"}"`,
	}
	malicious := `x"; touch ` + marker + `; echo "`
	tasks := map[string]*contract.TaskState{}
	if _, err := RunWorkflowSetup(prov, WorkflowHookVars{ResourceID: malicious, SessionName: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunWorkflowSetup: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("shellQuote did not prevent shell interpretation: injected command executed")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if string(got) != malicious {
		t.Errorf("downstream command received %q, want the literal resource id %q", got, malicious)
	}
}

func TestRunWorkflowSetup_MissingWorkspaceDirFails(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "wf", Setup: `echo '{"branch":"b"}'`}
	tasks := map[string]*contract.TaskState{}
	_, err := RunWorkflowSetup(prov, WorkflowHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected error for missing workspace_dir output")
	}
	if !strings.Contains(err.Error(), "workspace_dir") {
		t.Errorf("unexpected message: %v", err)
	}
	st := tasks[contract.WorkflowPseudoNodeID]
	if st == nil || st.Status != contract.TaskStatusFailed {
		t.Errorf("pseudo-node should be failed, got %+v", st)
	}
}

func TestRunWorkflowSetup_IdempotentSkip(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "wf", Setup: `echo should-not-run >&2; exit 1`}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd"},
		},
	}
	outputs, err := RunWorkflowSetup(prov, WorkflowHookVars{}, tasks, nil)
	if err != nil {
		t.Fatalf("produced pseudo-node must short-circuit: %v", err)
	}
	if outputs["workspace_dir"] != "/tmp/wd" {
		t.Errorf("outputs = %v", outputs)
	}
}

func TestRunWorkflowSetup_PrevSurvivesRetry(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "prev.txt")
	prov := config.WorkspaceProviderConfig{
		ID:    "wf",
		Setup: `echo "{{get .Prev "workspace_dir"}}" > ` + marker + `; echo '{"workspace_dir":"/tmp/new"}'`,
	}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusFailed,
			Outputs: map[string]any{"workspace_dir": "/tmp/old"},
		},
	}
	if _, err := RunWorkflowSetup(prov, WorkflowHookVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "/tmp/old" {
		t.Errorf(".Prev = %q, want /tmp/old", strings.TrimSpace(string(data)))
	}
}

func TestRunWorkflowSetup_OutputsSchemaEnforced(t *testing.T) {
	prov := config.WorkspaceProviderConfig{
		ID:    "wf",
		Setup: `echo '{"workspace_dir":"/tmp/x","branch":42}'`,
		OutputsSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"branch": map[string]any{"type": "string"},
			},
		},
	}
	tasks := map[string]*contract.TaskState{}
	_, err := RunWorkflowSetup(prov, WorkflowHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected schema violation")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestRunWorkflowCleanup_RunsAndMarksClean(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned.txt")
	prov := config.WorkspaceProviderConfig{
		ID:      "wf",
		Setup:   "echo '{}'",
		Cleanup: `echo "{{.Self.workspace_dir}}" > ` + marker,
	}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd"},
		},
	}
	if err := RunWorkflowCleanup(prov, WorkflowHookVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "/tmp/wd" {
		t.Errorf(".Self.workspace_dir = %q", strings.TrimSpace(string(data)))
	}
	st := tasks[contract.WorkflowPseudoNodeID]
	if st.Status != contract.TaskStatusCleaned {
		t.Errorf("status = %q, want cleaned", st.Status)
	}
	if st.Outputs["workspace_dir"] != "/tmp/wd" {
		t.Error("outputs must survive cleanup for .Prev on retry")
	}
}

// TestRunWorkflowCleanup_CleanupInputsReachTemplate pins the generic escape
// hatch a workspace provider's cleanup script reads its own intents through
// (e.g. the shipped github workspace provider's delete_branch key) — core
// forwards the map opaquely, so a new intent never needs a core vocabulary
// addition.
func TestRunWorkflowCleanup_CleanupInputsReachTemplate(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "delete_branch.txt")
	prov := config.WorkspaceProviderConfig{
		ID:      "wf",
		Setup:   "echo '{}'",
		Cleanup: `echo {{eq .CleanupInputs.delete_branch "true"}} > ` + marker,
	}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd"},
		},
	}
	vars := WorkflowHookVars{CleanupInputs: map[string]string{"delete_branch": "true"}}
	if err := RunWorkflowCleanup(prov, vars, tasks, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "true" {
		t.Errorf(".CleanupInputs.delete_branch rendered %q, want true", got)
	}
}

// TestRunWorkflowCleanup_CleanupInputsDefaultUnset pins that an intent no
// caller set renders as the map's zero value rather than a template error —
// a cleanup script reading one key must never have to special-case "no
// intents given".
func TestRunWorkflowCleanup_CleanupInputsDefaultUnset(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "delete_branch.txt")
	prov := config.WorkspaceProviderConfig{
		ID:      "wf",
		Setup:   "echo '{}'",
		Cleanup: `echo {{eq .CleanupInputs.delete_branch "true"}} > ` + marker,
	}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd"},
		},
	}
	if err := RunWorkflowCleanup(prov, WorkflowHookVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "false" {
		t.Errorf(".CleanupInputs.delete_branch rendered %q, want false when unset", got)
	}
}

func TestRunWorkflowCleanup_NoStateSkips(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "wf", Cleanup: "exit 1"}
	if err := RunWorkflowCleanup(prov, WorkflowHookVars{}, map[string]*contract.TaskState{}, nil); err != nil {
		t.Fatalf("no setup state must skip cleanly: %v", err)
	}
}

func TestRunWorkflowCleanup_FailureMarksFailed(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "wf", Cleanup: "exit 7"}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd"},
		},
	}
	err := RunWorkflowCleanup(prov, WorkflowHookVars{}, tasks, nil)
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if tasks[contract.WorkflowPseudoNodeID].Status != contract.TaskStatusFailed {
		t.Errorf("status = %q, want failed", tasks[contract.WorkflowPseudoNodeID].Status)
	}
}

// Node templates can reference the pseudo-node's outputs.
func TestRunSetup_ExposesWorkflowOutputs(t *testing.T) {
	resolved := []Resolved{{
		NodeID: "echoer",
		Scope:  config.TaskScopeSession,
		Setup:  `echo "{\"got\":\"{{.Workflow.outputs.branch}}\"}"`,
		Inputs: map[string]string{"wd": "{{.Workflow.outputs.workspace_dir}}"},
	}}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Scope:   contract.TaskScopeSession,
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workspace_dir": "/tmp/wd", "branch": "issue/9"},
		},
	}
	if err := RunSetup(context.Background(), resolved, SessionVars{}, tasks, nil); err != nil {
		t.Fatal(err)
	}
	st := tasks["echoer"]
	if st.Outputs["got"] != "issue/9" {
		t.Errorf(".Workflow.outputs.branch = %v", st.Outputs["got"])
	}
	if st.Inputs["wd"] != "/tmp/wd" {
		t.Errorf("input binding over .Workflow.outputs.workspace_dir = %v", st.Inputs["wd"])
	}
}
