package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// writeEnvironmentWorkflow gives the fixture workflow an environment: envToml
// (setup/exec/cleanup/outputs_schema — flat environment keys) lands in
// environments/<envID>.toml and the workflow gains `environment = "<envID>"`.
// Prepend, not append — mirrors writeSetupWorkflow.
func writeEnvironmentWorkflow(t *testing.T, cfg *config.Config, wfID, envID, envToml string) {
	t.Helper()
	envsDir := filepath.Join(cfg.BaseDir, "environments")
	if err := os.MkdirAll(envsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envsDir, envID+".toml"), []byte(envToml), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", wfID+".toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	header := "environment = \"" + envID + "\"\n"
	if err := os.WriteFile(wfPath, append([]byte(header), existing...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCreate_HostDegeneration_NoEnvironmentNode locks in the acceptance
// criterion: a workflow that declares no environment never gains an
// "@environment" state node, mirroring host_environment_test.go's
// task-package-level guarantee at the service/create layer.
func TestCreate_HostDegeneration_NoEnvironmentNode(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := store.Get("org/repo-1+wf")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if _, ok := s.Tasks[contract.EnvironmentPseudoNodeID]; ok {
		t.Errorf("host degeneration violated: %q present in Tasks", contract.EnvironmentPseudoNodeID)
	}
}

// TestCreate_EnvironmentLifecycle_SetupRunsAfterProviderBeforeTasks verifies
// the ordering acceptance criterion: provider setup -> environment setup ->
// task setup, with @environment's outputs schema-validated and visible to
// session tasks as .Environment.outputs.*.
func TestCreate_EnvironmentLifecycle_SetupRunsAfterProviderBeforeTasks(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo "{\"seen_marker\":\"{{.Environment.outputs.marker}}\"}"`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", `
setup = '''
[ -d "{{.WorkdirPath}}" ] && vis=yes || vis=no
echo "{\"marker\":\"m1\",\"workdir_visible\":\"$vis\"}"
'''
exec = '"$@"'
[outputs_schema]
type = "object"
required = ["marker"]
`)

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/2"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := store.Get("org/repo-2+wf")
	if s == nil {
		t.Fatal("session not persisted")
	}
	envState := s.Tasks[contract.EnvironmentPseudoNodeID]
	if envState == nil || envState.Status != contract.TaskStatusProduced {
		t.Fatalf("environment pseudo-node = %+v, want produced", envState)
	}
	if envState.Outputs["marker"] != "m1" {
		t.Errorf("environment outputs = %v", envState.Outputs)
	}
	// The workdir (provider output) must already exist when environment
	// setup runs: provider -> environment ordering.
	if envState.Outputs["workdir_visible"] != "yes" {
		t.Errorf("workdir_visible = %v, want yes (provider setup must run before environment setup)", envState.Outputs["workdir_visible"])
	}
	probe := s.Tasks["probe"]
	if probe == nil || probe.Status != contract.TaskStatusProduced {
		t.Fatalf("probe = %+v, want produced", probe)
	}
	if probe.Outputs["seen_marker"] != "m1" {
		t.Errorf(".Environment.outputs.marker not visible to task setup: %v", probe.Outputs)
	}
}

// TestCreate_EnvironmentSetupFailureFailsClosed verifies fail-closed error
// handling: an environment setup failure must abort before any session task
// setup runs, leaving no partial task state.
func TestCreate_EnvironmentSetupFailureFailsClosed(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", `
setup = "exit 7"
exec  = '"$@"'
`)

	_, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/3"})
	if err == nil {
		t.Fatal("expected environment setup failure to abort Create")
	}
	s := store.Get("org/repo-3+wf")
	if s == nil {
		t.Fatal("session should still be persisted (inspectable/retryable)")
	}
	envState := s.Tasks[contract.EnvironmentPseudoNodeID]
	if envState == nil || envState.Status != contract.TaskStatusFailed {
		t.Fatalf("environment pseudo-node = %+v, want failed", envState)
	}
	if _, ok := s.Tasks["probe"]; ok {
		t.Errorf("session task setup must not have run after environment setup failed: %+v", s.Tasks["probe"])
	}
}

// TestCreate_EnvironmentOutputsSchemaViolationFailsClosed verifies that
// setup outputs failing the environment's declared outputs_schema fail
// exactly like a script failure: fail-closed, no session task setup runs.
func TestCreate_EnvironmentOutputsSchemaViolationFailsClosed(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", `
setup = "echo '{}'"
exec  = '"$@"'
[outputs_schema]
type = "object"
required = ["marker"]
`)

	_, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/6"})
	if err == nil {
		t.Fatal("expected outputs_schema violation to abort Create")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("unexpected error: %v", err)
	}
	s := store.Get("org/repo-6+wf")
	if s == nil {
		t.Fatal("session should still be persisted")
	}
	envState := s.Tasks[contract.EnvironmentPseudoNodeID]
	if envState == nil || envState.Status != contract.TaskStatusFailed {
		t.Fatalf("environment pseudo-node = %+v, want failed", envState)
	}
	if _, ok := s.Tasks["probe"]; ok {
		t.Errorf("session task setup must not have run after schema violation: %+v", s.Tasks["probe"])
	}
}

// TestExecutor_TaskExecutionEnvironmentRoutesThroughEnvironmentExec verifies
// the acceptance criterion that an execution="environment" task script runs
// through the environment's `exec` wrapper: the wrapper script appends a
// marker line before forwarding argv, so a marker file only exists if the
// wrapper actually ran.
func TestExecutor_TaskExecutionEnvironmentRoutesThroughEnvironmentExec(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	execLog := filepath.Join(t.TempDir(), "exec.log")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{"ok":"yes"}'`, execution: config.ExecutionEnvironment}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", fmt.Sprintf(`
setup = "echo '{}'"
exec  = '''
echo via-wrapper >> %s
"$@"
'''
`, execLog))

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/4"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := store.Get("org/repo-4+wf")
	probe := s.Tasks["probe"]
	if probe == nil || probe.Status != contract.TaskStatusProduced || probe.Outputs["ok"] != "yes" {
		t.Fatalf("probe = %+v, want produced with ok=yes (proves argv still ran correctly through the wrapper)", probe)
	}
	data, err := os.ReadFile(execLog)
	if err != nil {
		t.Fatalf("exec wrapper marker missing: %v (task did not route through environment exec)", err)
	}
	if strings.TrimSpace(string(data)) != "via-wrapper" {
		t.Errorf("exec log = %q", data)
	}
}

// TestDestroy_EnvironmentCleanupOrdering verifies the cleanup acceptance
// criterion: run tasks -> session tasks -> environment cleanup -> provider
// cleanup, by having each stage append to a shared ordered log.
func TestDestroy_EnvironmentCleanupOrdering(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	order := filepath.Join(t.TempDir(), "order.log")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{}'`, cleanup: fmt.Sprintf(`echo task >> %s`, order)}},
		[]nodeFixture{{id: "probe"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup   = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
cleanup = '''
echo provider >> %s
'''
`, workdir, workdir, order)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", fmt.Sprintf(`
setup   = "echo '{}'"
exec    = '"$@"'
cleanup = '''
echo environment >> %s
'''
`, order))

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/5"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Destroy(cfg, store, DestroyParams{Identifier: "org/repo-5+wf"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	data, err := os.ReadFile(order)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(strings.TrimSpace(string(data)))
	want := []string{"task", "environment", "provider"}
	if len(lines) != len(want) {
		t.Fatalf("cleanup order = %v, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("cleanup order = %v, want %v", lines, want)
			break
		}
	}
}

// TestUp_EnvironmentSetupFailedFailsClosed is the direct regression test for
// the fail-closed gap a reviewer flagged: a session whose Create already
// failed at environment setup (persisted, retryable, @environment == failed)
// must not let a later bare-name `plect up` silently run an
// execution="environment" run-scoped task on host using stale/missing
// environment outputs.
func TestUp_EnvironmentSetupFailedFailsClosed(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "runner", scope: "run", setup: `echo '{}'`, execution: config.ExecutionEnvironment}},
		[]nodeFixture{{id: "runner"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", `
setup = "exit 7"
exec  = '"$@"'
`)

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/8"}); err == nil {
		t.Fatal("expected Create to fail at environment setup")
	}
	s := store.Get("org/repo-8+wf")
	if s == nil {
		t.Fatal("session should still be persisted (inspectable/retryable)")
	}
	if envState := s.Tasks[contract.EnvironmentPseudoNodeID]; envState == nil || envState.Status != contract.TaskStatusFailed {
		t.Fatalf("environment pseudo-node = %+v, want failed", envState)
	}

	_, upErr := Up(cfg, store, UpParams{Identifier: "org/repo-8+wf"})
	if upErr == nil {
		t.Fatal("expected Up to fail closed against a session whose environment previously failed")
	}
	if !strings.Contains(upErr.Error(), "environment executor") {
		t.Errorf("unexpected error: %v", upErr)
	}
	s = store.Get("org/repo-8+wf")
	// The task is recorded failed (RunSetup's normal fail-closed bookkeeping)
	// — the point being tested is that it never reached TaskStatusProduced,
	// i.e. its setup script never actually ran (on host or otherwise).
	if runner := s.Tasks["runner"]; runner == nil || runner.Status == contract.TaskStatusProduced {
		t.Errorf("run-scoped execution=\"environment\" task must not have run: %+v", runner)
	}
}

// TestTaskSetup_EnvironmentSetupFailedFailsClosed mirrors
// TestUp_EnvironmentSetupFailedFailsClosed for the dynamic `plect task setup`
// path: a task declaring execution="environment" must not silently
// instantiate against the host when the session's environment previously
// failed.
func TestTaskSetup_EnvironmentSetupFailedFailsClosed(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")

	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{
			{id: "runner", scope: "session", setup: `echo '{}'`},
			// session-scoped (not run-scoped): instantiable any time, so this
			// test exercises the environment-executor fail-closed check
			// specifically, not the unrelated "run scope not up" guard.
			{id: "dynamic_worker", scope: "session", setup: `echo '{}'`, execution: config.ExecutionEnvironment},
		},
		[]nodeFixture{{id: "runner"}})
	writeSetupWorkflow(t, cfg, "wf", fmt.Sprintf(`
setup = '''
mkdir -p %s
echo '{"workdir":"%s"}'
'''
`, workdir, workdir)+githubResolver)
	writeEnvironmentWorkflow(t, cfg, "wf", "docker", `
setup = "exit 7"
exec  = '"$@"'
`)

	if _, err := Create(cfg, store, CreateParams{URL: "https://github.com/org/repo/issues/9"}); err == nil {
		t.Fatal("expected Create to fail at environment setup")
	}

	_, err := TaskSetup(cfg, store, TaskSetupParams{TaskID: "dynamic_worker", SessionName: "org/repo-9+wf"})
	if err == nil {
		t.Fatal("expected TaskSetup to fail closed against a session whose environment previously failed")
	}
	if !strings.Contains(err.Error(), "environment executor") {
		t.Errorf("unexpected error: %v", err)
	}
}
