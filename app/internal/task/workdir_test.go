package task

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func workdirWorkflow(nodes ...config.WorkflowNode) config.WorkflowFile {
	return config.WorkflowFile{
		ID:      "env",
		Workdir: &lang.WorkdirRef{Node: "checkout", Key: "workspace_dir"},
		Nodes:   nodes,
	}
}

func workdirDefs(ids ...string) map[string]config.TaskDefinition {
	defs := make(map[string]config.TaskDefinition, len(ids))
	for _, id := range ids {
		defs[id] = config.TaskDefinition{ID: id, Scope: "session", Setup: shellStub("true")}
	}
	return defs
}

func prepIDs(plan *Plan) []string {
	var ids []string
	for _, r := range plan.UpOrder() {
		if r.Prep {
			ids = append(ids, r.NodeID)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestCompileWorkflow_Workdir_PreparationNodesAreTheProducerAndItsPrerequisites(t *testing.T) {
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "token", Uses: "token"},
		config.WorkflowNode{ID: "checkout", Uses: "checkout", Inputs: map[string]*lang.Value{
			"token": fromValue("nodes.token.outputs.value"),
		}},
		config.WorkflowNode{ID: "agent", Uses: "agent"},
		config.WorkflowNode{ID: "orchestrator", Uses: "orchestrator", Inputs: map[string]*lang.Value{
			"workspace_dir": fromValue("nodes.checkout.outputs.workspace_dir"),
		}},
	)
	plan, err := CompileWorkflow(wf, workdirDefs("token", "checkout", "agent", "orchestrator"))
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if got, want := prepIDs(plan), []string{"checkout", "token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preparation nodes = %v, want %v", got, want)
	}
	byID := map[string]Resolved{}
	for _, r := range plan.UpOrder() {
		byID[r.NodeID] = r
	}
	if got := byID["token"].DependsOn; len(got) != 0 {
		t.Errorf("token.DependsOn = %v, want none: a preparation node gains no default edge", got)
	}
	if got := byID["checkout"].DependsOn; !reflect.DeepEqual(got, []string{"token"}) {
		t.Errorf("checkout.DependsOn = %v, want [token]", got)
	}
	for _, id := range []string{"agent", "orchestrator"} {
		r := byID[id]
		if r.Prep {
			t.Errorf("%s is a preparation node", id)
		}
		if !hasDep(r.DependsOn, "checkout") {
			t.Errorf("%s.DependsOn = %v, want it to depend on checkout", id, r.DependsOn)
		}
		if r.Workdir == nil || r.Workdir.Node != "checkout" || r.Workdir.Key != "workspace_dir" {
			t.Errorf("%s.Workdir = %+v, want checkout.workspace_dir", id, r.Workdir)
		}
	}
	if byID["orchestrator"].DependsOn[0] != "checkout" || len(byID["orchestrator"].DependsOn) != 1 {
		t.Errorf("orchestrator.DependsOn = %v: an explicit edge to the producer must not be duplicated", byID["orchestrator"].DependsOn)
	}
}

func TestCompileWorkflow_Workdir_PreparationSetIsDerivedBeforeDefaultEdges(t *testing.T) {
	// token feeds checkout; a default edge from token to checkout would be a
	// cycle, so a set derived after the default edges could never be built.
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "checkout", Uses: "checkout", Inputs: map[string]*lang.Value{
			"token": fromValue("nodes.token.outputs.value"),
		}},
		config.WorkflowNode{ID: "token", Uses: "token"},
	)
	plan, err := CompileWorkflow(wf, workdirDefs("token", "checkout"))
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if got, want := prepIDs(plan), []string{"checkout", "token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preparation nodes = %v, want %v", got, want)
	}
}

func TestCompileWorkflow_Workdir_PublicOutputReadsCountAsExplicitEdges(t *testing.T) {
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "checkout", Uses: "checkout", Inputs: map[string]*lang.Value{
			"endpoint": fromValue("workflow.outputs.endpoint"),
		}},
		config.WorkflowNode{ID: "server", Uses: "server"},
	)
	wf.Outputs = map[string]*lang.Value{"endpoint": fromValue("nodes.server.outputs.endpoint")}
	plan, err := CompileWorkflow(wf, workdirDefs("checkout", "server"))
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	if got, want := prepIDs(plan), []string{"checkout", "server"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preparation nodes = %v, want %v: a public output the producer reads is a prerequisite", got, want)
	}
}

func TestCompileWorkflow_WithoutWorkdir_NoNodeHasADefaultDirectory(t *testing.T) {
	wf := config.WorkflowFile{ID: "env", Nodes: []config.WorkflowNode{
		{ID: "checkout", Uses: "checkout"},
		{ID: "agent", Uses: "agent"},
	}}
	plan, err := CompileWorkflow(wf, workdirDefs("checkout", "agent"))
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	for _, r := range plan.UpOrder() {
		if r.Prep || r.Workdir != nil || len(r.DependsOn) != 0 {
			t.Errorf("node %s = prep %v workdir %+v deps %v, want no default directory and no added edge", r.NodeID, r.Prep, r.Workdir, r.DependsOn)
		}
	}
}

func TestCompileWorkflow_Workdir_RejectsAProducerThatIsNotANode(t *testing.T) {
	wf := workdirWorkflow(config.WorkflowNode{ID: "agent", Uses: "agent"})
	_, err := CompileWorkflow(wf, workdirDefs("agent"))
	if err == nil || !strings.Contains(err.Error(), `workdir`) || !strings.Contains(err.Error(), `"checkout"`) {
		t.Fatalf("err = %v, want a workdir diagnostic naming the unknown node %q", err, "checkout")
	}
}

// homeDir points $HOME at a fresh directory, so a preparation node's
// directory is observable and distinct from the process's cwd.
func homeDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func workdirStubPlan(t *testing.T, workdir string) *Plan {
	t.Helper()
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "checkout", Uses: "checkout"},
		config.WorkflowNode{ID: "agent", Uses: "agent"},
	)
	defs := map[string]config.TaskDefinition{
		"checkout": {ID: "checkout", Scope: "session", Setup: shellStub(`echo '{"workspace_dir":"` + workdir + `"}'`)},
		"agent":    {ID: "agent", Scope: "session", Setup: shellStub(`true`), Cleanup: shellStub(`true`)},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	return plan
}

func TestRunSetup_Workdir_PreparationNodesRunInHomeAndOthersInTheWorkdir(t *testing.T) {
	home := homeDir(t)
	workdir := t.TempDir()
	spy := &effect.SpyExecutor{Stdout: []byte(`{"workspace_dir":"` + workdir + `"}`)}
	t.Cleanup(effect.UseExecutor(spy))
	plan := workdirStubPlan(t, workdir)
	tasks := map[string]*contract.TaskState{}

	if err := RunSetup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(spy.Requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(spy.Requests))
	}
	if got := spy.Requests[0].Dir; got != home {
		t.Errorf("checkout ran in %q, want the user's home %q", got, home)
	}
	if got := spy.Requests[1].Dir; got != workdir {
		t.Errorf("agent ran in %q, want the workdir %q", got, workdir)
	}
	if tasks["checkout"].Directory != home || tasks["agent"].Directory != workdir {
		t.Errorf("recorded directories = %q, %q; want %q, %q", tasks["checkout"].Directory, tasks["agent"].Directory, home, workdir)
	}
}

func TestRunSetup_Workdir_MustBeAnAbsoluteDirectoryPath(t *testing.T) {
	homeDir(t)
	spy := &effect.SpyExecutor{Stdout: []byte(`{"workspace_dir":"relative/dir"}`)}
	t.Cleanup(effect.UseExecutor(spy))
	plan := workdirStubPlan(t, "relative/dir")
	tasks := map[string]*contract.TaskState{}

	err := RunSetup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want a failure naming the absolute-path requirement", err)
	}
	if st := tasks["agent"]; st != nil && st.Status == contract.TaskStatusProduced {
		t.Errorf("agent was produced against a relative workdir")
	}
}

func TestRunSetup_WithoutWorkdir_NodesRunWithNoExplicitDirectory(t *testing.T) {
	homeDir(t)
	spy := &effect.SpyExecutor{Stdout: []byte(`{}`)}
	t.Cleanup(effect.UseExecutor(spy))
	wf := config.WorkflowFile{ID: "env", Nodes: []config.WorkflowNode{{ID: "a", Uses: "a"}}}
	plan, err := CompileWorkflow(wf, workdirDefs("a"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := spy.Requests[0].Dir; got != "" {
		t.Errorf("dir = %q, want none", got)
	}
	if tasks["a"].Directory != "" {
		t.Errorf("recorded directory = %q, want none", tasks["a"].Directory)
	}
}

func TestRunCleanup_RunsInTheDirectoryItsSetupUsed(t *testing.T) {
	homeDir(t)
	spy := &effect.SpyExecutor{Stdout: []byte(`{}`)}
	t.Cleanup(effect.UseExecutor(spy))
	// The declared workdir changed after setup: cleanup must not follow it.
	plan := workdirStubPlan(t, "/somewhere/else")
	tasks := map[string]*contract.TaskState{
		"checkout": {Scope: "session", Status: contract.TaskStatusProduced, Directory: "/recorded/home", Outputs: map[string]any{"workspace_dir": "/somewhere/else"}},
		"agent":    {Scope: "session", Status: contract.TaskStatusProduced, Directory: "/recorded/workdir"},
	}
	if err := RunCleanup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	var dirs []string
	for _, req := range spy.Requests {
		dirs = append(dirs, req.Dir)
	}
	if want := []string{"/recorded/workdir"}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("cleanup directories = %v, want %v (checkout declares no cleanup)", dirs, want)
	}
}

func realShellWorkdirPlan(t *testing.T, workdir, marker string) *Plan {
	t.Helper()
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "checkout", Uses: "checkout"},
		config.WorkflowNode{ID: "agent", Uses: "agent"},
	)
	defs := map[string]config.TaskDefinition{
		"checkout": {ID: "checkout", Scope: "session", Setup: shellStub(`mkdir -p ` + workdir + ` && echo '{"workspace_dir":"` + workdir + `"}'`)},
		"agent": {
			ID: "agent", Scope: "session",
			Setup:   shellStub(`echo '{}'`),
			Cleanup: shellStub(`echo cleaned > ` + marker),
			Health:  healthStub(`true`),
		},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	return plan
}

func TestRunSetup_VanishedWorkdir_AliveFailureInvalidatesTheNodeAndCleanupFailsInPlace(t *testing.T) {
	homeDir(t)
	workdir := filepath.Join(t.TempDir(), "work")
	marker := filepath.Join(t.TempDir(), "cleaned.marker")
	plan := realShellWorkdirPlan(t, workdir, marker)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if tasks["agent"].Status != contract.TaskStatusProduced {
		t.Fatalf("agent.Status = %q, want produced", tasks["agent"].Status)
	}
	if err := os.RemoveAll(workdir); err != nil {
		t.Fatal(err)
	}

	err := RunSetup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil)
	if err == nil {
		t.Fatalf("re-up with a vanished workdir succeeded")
	}
	agent := tasks["agent"]
	if agent.Status != contract.TaskStatusFailed {
		t.Errorf("agent.Status = %q, want failed: the record and its release obligation remain", agent.Status)
	}
	if agent.Directory != workdir {
		t.Errorf("agent.Directory = %q, want the recorded %q", agent.Directory, workdir)
	}
	if agent.Error == "" {
		t.Errorf("agent.Error is empty, want the failed launch recorded")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("cleanup ran somewhere else instead of failing in the recorded directory")
	}
	if _, statErr := os.Stat(workdir); statErr == nil {
		t.Errorf("the vanished workdir was recreated")
	}
}

func TestRunCleanup_VanishedDirectoryIsACleanupFailure(t *testing.T) {
	homeDir(t)
	missing := filepath.Join(t.TempDir(), "gone")
	marker := filepath.Join(t.TempDir(), "cleaned.marker")
	plan := realShellWorkdirPlan(t, missing, marker)
	tasks := map[string]*contract.TaskState{
		"agent": {Scope: "session", Status: contract.TaskStatusProduced, Directory: missing},
	}
	err := RunCleanup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil)
	if err == nil {
		t.Fatal("cleanup in a vanished directory succeeded")
	}
	if tasks["agent"].Status != contract.TaskStatusFailed || tasks["agent"].Error == "" {
		t.Errorf("agent = %+v, want a recorded cleanup failure", tasks["agent"])
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("cleanup ran somewhere else instead of failing in the recorded directory")
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Errorf("cleanup recreated the directory")
	}
}

func cleanupBlockingPlan(t *testing.T, markerDir string) *Plan {
	t.Helper()
	wf := workdirWorkflow(
		config.WorkflowNode{ID: "checkout", Uses: "checkout"},
		config.WorkflowNode{ID: "agent", Uses: "agent"},
		config.WorkflowNode{ID: "unrelated", Uses: "unrelated"},
	)
	defs := map[string]config.TaskDefinition{
		"checkout":  {ID: "checkout", Scope: "session", Setup: shellStub(`true`), Cleanup: shellStub(`touch ` + filepath.Join(markerDir, "checkout-released"))},
		"agent":     {ID: "agent", Scope: "session", Setup: shellStub(`true`), Cleanup: shellStub(`exit 7`)},
		"unrelated": {ID: "unrelated", Scope: "session", Setup: shellStub(`true`), Cleanup: shellStub(`touch ` + filepath.Join(markerDir, "unrelated-released"))},
	}
	plan, err := CompileWorkflow(wf, defs)
	if err != nil {
		t.Fatalf("CompileWorkflow: %v", err)
	}
	return plan
}

func producedForCleanup(plan *Plan, dir string) map[string]*contract.TaskState {
	tasks := map[string]*contract.TaskState{}
	for _, r := range plan.UpOrder() {
		tasks[r.NodeID] = &contract.TaskState{Scope: r.Scope, Status: contract.TaskStatusProduced, Directory: dir, DependsOn: r.DependsOn}
	}
	return tasks
}

func TestRunCleanup_FailedDependentKeepsItsPrerequisitesRetained(t *testing.T) {
	dir := t.TempDir()
	markers := t.TempDir()
	plan := cleanupBlockingPlan(t, markers)
	tasks := producedForCleanup(plan, dir)

	err := RunCleanup(context.Background(), plan.Session, SessionVars{Name: "x"}, tasks, nil)
	if err == nil {
		t.Fatal("cleanup with a failing node succeeded")
	}
	if tasks["agent"].Status != contract.TaskStatusFailed {
		t.Errorf("agent.Status = %q, want failed", tasks["agent"].Status)
	}
	if _, statErr := os.Stat(filepath.Join(markers, "checkout-released")); statErr == nil {
		t.Errorf("the checkout was released although the node depending on it failed its cleanup")
	}
	if tasks["checkout"].Status != contract.TaskStatusProduced {
		t.Errorf("checkout.Status = %q, want produced: its release waits for the dependent's", tasks["checkout"].Status)
	}
	if _, statErr := os.Stat(filepath.Join(markers, "unrelated-released")); statErr == nil {
		return
	}
	t.Errorf("an unrelated node was not released: a failure must not block work that does not depend on it")
}

func TestRunCleanup_ReleaseAfterFailureAttemptsPrerequisitesToo(t *testing.T) {
	dir := t.TempDir()
	markers := t.TempDir()
	plan := cleanupBlockingPlan(t, markers)
	tasks := producedForCleanup(plan, dir)

	err := RunCleanup(context.Background(), plan.Session, SessionVars{Name: "x", ReleaseAfterFailure: true}, tasks, nil)
	if err == nil {
		t.Fatal("cleanup with a failing node succeeded")
	}
	if _, statErr := os.Stat(filepath.Join(markers, "checkout-released")); statErr != nil {
		t.Errorf("the explicit discard did not attempt the checkout's release: %v", statErr)
	}
	if tasks["checkout"].Status != contract.TaskStatusCleaned {
		t.Errorf("checkout.Status = %q, want cleaned", tasks["checkout"].Status)
	}
}
