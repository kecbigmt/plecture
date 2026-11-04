package task

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/procid"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// guardedRuntimeChain is the shape docs/language/effects.md shows: an outer
// layer that adds a guard around a runtime whose own declaration owns the
// process probe, and a public contract that binds the runtime's pid through.
func guardedRuntimeChain(t *testing.T, innerSetup string, outerCleanup, innerCleanup string) []Resolved {
	t.Helper()
	outer := config.TaskDefinition{
		ID: "guarded", Scope: "run",
		Setup:         shellStub(`echo '{"guard":"g"}'`),
		Cleanup:       shellStub(outerCleanup),
		Health:        &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionNoop}},
		LocalsSchema:  objectSchema(map[string]any{"guard": map[string]any{"type": "string"}}),
		OutputsBind:   map[string]*lang.Value{"pid": fromValue("inner.outputs.pid")},
		OutputsSchema: objectSchema(map[string]any{"pid": map[string]any{"type": "integer"}}),
	}
	inner := config.TaskDefinition{
		ID: "runtime", Scope: "run",
		Setup:   shellStub(innerSetup),
		Cleanup: shellStub(innerCleanup),
		Health:  &config.HealthConfig{Alive: processAlive("pid")},
	}
	return nestedPlan(t, outer, inner)
}

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
}

func killOnCleanup(t *testing.T, pid int) {
	t.Helper()
	t.Cleanup(func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() })
}

func TestRunSetup_NestedChainRecordsTheProcessIdentityOfItsDeclaringLayer(t *testing.T) {
	requireBash(t)
	plan := guardedRuntimeChain(t, spawnWorkerSetup, "true", "true")
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	st := tasks["guarded"]
	pid, ok := asPID(st.Outputs["pid"])
	if !ok {
		t.Fatalf("outputs.pid = %v, want a pid projected through the chain", st.Outputs["pid"])
	}
	killOnCleanup(t, pid)
	if st.Process == nil || st.Process.PID != pid || st.Process.Start <= 0 {
		t.Fatalf("Process = %+v, want an identity for pid %d", st.Process, pid)
	}
}

func TestRunSetup_NestedChainReadsThePidFromTheDeclaringLayersOwnContract(t *testing.T) {
	requireBash(t)
	outer := config.TaskDefinition{
		ID: "wrapper", Scope: "run",
		OutputsBind:   map[string]*lang.Value{"agent_pid": fromValue("inner.outputs.pid")},
		OutputsSchema: objectSchema(map[string]any{"agent_pid": map[string]any{"type": "integer"}}),
	}
	inner := config.TaskDefinition{
		ID: "runtime", Scope: "run", Setup: shellStub(spawnWorkerSetup), Cleanup: shellStub("true"),
		Health: &config.HealthConfig{Alive: processAlive("pid")},
	}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	st := tasks["wrapper"]
	pid, _ := asPID(st.Outputs["agent_pid"])
	killOnCleanup(t, pid)
	if st.Process == nil || st.Process.PID != pid {
		t.Fatalf("Process = %+v, want an identity for pid %d", st.Process, pid)
	}
}

func TestRunSetup_NestedChainWithoutAProcessProbeRecordsNoIdentity(t *testing.T) {
	requireBash(t)
	outer := config.TaskDefinition{ID: "outer", Scope: "run"}
	inner := config.TaskDefinition{ID: "inner", Scope: "run", Setup: shellStub(`echo '{"pid":1}'`), Cleanup: shellStub("true")}
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), nestedPlan(t, outer, inner), SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if got := tasks["outer"].Process; got != nil {
		t.Fatalf("Process = %+v, want none", got)
	}
}

func TestRunSetup_NestedChainWhoseProcessIsAlreadyGoneFailsAndKeepsItsLayersForCleanup(t *testing.T) {
	requireBash(t)
	plan := guardedRuntimeChain(t, `echo '{"pid":2147483000}'`, "true", "true")
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("RunSetup error = %v, want a not-running failure", err)
	}
	st := tasks["guarded"]
	if st == nil || st.Status != contract.TaskStatusFailed || len(st.Layers) != 2 {
		t.Fatalf("guarded = %+v, want a failed record that keeps both produced layers for the unwind", st)
	}
}

func TestRunSetup_NestedChainIsSkippedWhileItsRecordedProcessIsLive(t *testing.T) {
	requireBash(t)
	plan := guardedRuntimeChain(t, spawnWorkerSetup, "true", "true")
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	st := tasks["guarded"]
	pid, _ := asPID(st.Outputs["pid"])
	killOnCleanup(t, pid)
	execID := st.ExecutionID

	if err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if tasks["guarded"] != st || tasks["guarded"].ExecutionID != execID {
		t.Fatal("a live recorded process must be skipped, not rebuilt")
	}
}

func TestRunSetup_NestedChainIsRebuiltWhenItsPidIsHeldByAnotherProcess(t *testing.T) {
	requireBash(t)
	marker := t.TempDir() + "/setup-ran"
	plan := guardedRuntimeChain(t, "touch "+marker+"\n"+spawnWorkerSetup, "true", "true")
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	pid, _ := asPID(tasks["guarded"].Outputs["pid"])
	killOnCleanup(t, pid)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	tasks["guarded"].Process.Start += 1000
	if err := RunSetup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("setup did not re-run for a pid held by a different process")
	}
	if newPid, _ := asPID(tasks["guarded"].Outputs["pid"]); newPid != pid {
		killOnCleanup(t, newPid)
	}
}

func TestVerifyLiveness_NestedChainFailsForADeadProcessAndPassesForTheRecordedOne(t *testing.T) {
	self := os.Getpid()
	identity, err := procid.Capture(self)
	if err != nil {
		t.Fatal(err)
	}
	plan := guardedRuntimeChain(t, "true", "true", "true")
	layers := []contract.LayerState{
		{EffectID: "guarded", Status: contract.TaskStatusProduced},
		{EffectID: "runtime", Status: contract.TaskStatusProduced, Outputs: map[string]any{"pid": self}},
	}
	for name, tc := range map[string]struct {
		pid     int
		process *contract.ProcessIdentity
		wantErr string
	}{
		"the recorded process":     {self, &identity, ""},
		"a dead pid":               {exitedPid(t), &identity, "recorded"},
		"a reused pid":             {self, &contract.ProcessIdentity{PID: self, Start: identity.Start + 1000, Boot: identity.Boot}, "different process"},
		"a record from an old run": {self, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			existing := &contract.TaskState{
				Status:  contract.TaskStatusProduced,
				Outputs: map[string]any{"pid": tc.pid},
				Layers:  append([]contract.LayerState(nil), layers...),
				Process: tc.process,
			}
			existing.Layers[1].Outputs = map[string]any{"pid": tc.pid}
			err := verifyLiveness(context.Background(), plan[0], SessionVars{Name: "s"}, existing)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyLiveness: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifyLiveness error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestExecuteTaskSetup_NestedChainRecordsProcessIdentity(t *testing.T) {
	requireBash(t)
	plan := guardedRuntimeChain(t, spawnWorkerSetup, "true", "true")
	got, err := ExecuteTaskSetup(context.Background(), plan[0], nil, SessionVars{Name: "s"}, nil)
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	pid, _ := asPID(got.Outputs["pid"])
	killOnCleanup(t, pid)
	if got.Process == nil || got.Process.PID != pid {
		t.Fatalf("Process = %+v, want an identity for pid %d", got.Process, pid)
	}
}

func TestExecuteTaskSetup_NestedChainWhoseProcessIsGoneFailsAndKeepsLayers(t *testing.T) {
	requireBash(t)
	plan := guardedRuntimeChain(t, `echo '{"pid":2147483000}'`, "true", "true")
	got, err := ExecuteTaskSetup(context.Background(), plan[0], nil, SessionVars{Name: "s"}, nil)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("ExecuteTaskSetup error = %v, want a not-running failure", err)
	}
	if len(got.Layers) != 2 {
		t.Fatalf("Layers = %+v, want both produced layers kept for the unwind", got.Layers)
	}
}

func TestRunCleanup_NestedChainHandsTheIdentityToTheDeclaringLayerOnly(t *testing.T) {
	requireBash(t)
	identity := contract.ProcessIdentity{PID: 4242, Start: 98765, Boot: "boot-a"}
	dir := t.TempDir()
	bindIdentity := func(out string) map[string]*lang.Value {
		return map[string]*lang.Value{
			"process_identity": {Form: lang.FormFrom, From: "self.process.identity", Optional: true},
			"out":              {Form: lang.FormLiteral, Literal: out},
		}
	}
	record := `printf '%s' "${process_identity-absent}" > "$out"`
	outer := config.TaskDefinition{
		ID: "guarded", Scope: "run",
		Cleanup:       &lang.Action{Type: lang.ActionShell, Script: record, Bind: bindIdentity(dir + "/outer")},
		OutputsBind:   map[string]*lang.Value{"pid": fromValue("inner.outputs.pid")},
		OutputsSchema: objectSchema(map[string]any{"pid": map[string]any{"type": "integer"}}),
	}
	inner := config.TaskDefinition{
		ID: "runtime", Scope: "run",
		Cleanup: &lang.Action{Type: lang.ActionShell, Script: record, Bind: bindIdentity(dir + "/inner")},
		Health:  &config.HealthConfig{Alive: processAlive("pid")},
	}
	plan := nestedPlan(t, outer, inner)
	tasks := map[string]*contract.TaskState{
		"guarded": {
			Scope: "run", Status: contract.TaskStatusProduced, Process: &identity,
			Outputs: map[string]any{"pid": 4242},
			Layers: []contract.LayerState{
				{EffectID: "guarded", Status: contract.TaskStatusProduced},
				{EffectID: "runtime", Status: contract.TaskStatusProduced, Outputs: map[string]any{"pid": 4242}},
			},
		},
	}
	if err := RunCleanup(context.Background(), plan, SessionVars{Name: "s"}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	for file, want := range map[string]string{"inner": procid.Encode(identity), "outer": "absent"} {
		got, err := os.ReadFile(dir + "/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s layer cleanup saw process_identity %q, want %q", file, got, want)
		}
	}
}
