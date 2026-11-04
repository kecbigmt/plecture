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

func processAlive(key string) *lang.Action {
	return &lang.Action{Type: lang.ActionProcess, Process: &lang.Value{Form: lang.FormFrom, From: "self.outputs." + key}}
}

func exitedPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestCaptureProcess(t *testing.T) {
	self := os.Getpid()
	tests := []struct {
		name    string
		health  *config.HealthConfig
		outputs map[string]any
		want    bool
		wantErr string
	}{
		{"no health table records nothing", nil, map[string]any{"pid": self}, false, ""},
		{"a shell alive probe records nothing", &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionShell, Script: "true"}}, map[string]any{"pid": self}, false, ""},
		{"a noop alive probe records nothing", &config.HealthConfig{Alive: &lang.Action{Type: lang.ActionNoop}}, map[string]any{"pid": self}, false, ""},
		{"an integer pid is recorded", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": self}, true, ""},
		{"a float pid from JSON is recorded", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": float64(self)}, true, ""},
		{"a decimal-string pid is recorded", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": strconv.Itoa(self)}, true, ""},
		{"a missing output fails setup", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{}, false, "resolved to nothing"},
		{"a non-integer output fails setup", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": "abc"}, false, "not a process id"},
		{"a fractional pid fails setup", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": 12.5}, false, "not a process id"},
		{"a pid that is not running fails setup", &config.HealthConfig{Alive: processAlive("pid")}, map[string]any{"pid": exitedPid(t)}, false, "not running"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CaptureProcess(tc.health, tc.outputs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("CaptureProcess error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CaptureProcess: %v", err)
			}
			if (got != nil) != tc.want {
				t.Fatalf("CaptureProcess = %+v, want recorded = %v", got, tc.want)
			}
			if got != nil && got.PID != self {
				t.Errorf("recorded pid = %d, want %d", got.PID, self)
			}
		})
	}
}

func TestRunAliveProbe_ProcessIdentity(t *testing.T) {
	self := os.Getpid()
	identity, err := procid.Capture(self)
	if err != nil {
		t.Fatal(err)
	}
	reused := identity
	reused.Start += 1000
	otherPid := identity
	otherPid.PID = 1

	dead := exitedPid(t)
	deadRecord := identity
	deadRecord.PID = dead

	tests := []struct {
		name    string
		action  *lang.Action
		self    map[string]any
		process *contract.ProcessIdentity
		wantErr string
	}{
		{"the recorded process passes", processAlive("pid"), map[string]any{"pid": self}, &identity, ""},
		{"a pid reused by another process fails", processAlive("pid"), map[string]any{"pid": self}, &reused, "different process"},
		{"a record without a start time is judged by pid existence", processAlive("pid"), map[string]any{"pid": self}, nil, ""},
		{"a dead pid fails without a start time", processAlive("pid"), map[string]any{"pid": dead}, nil, "not running"},
		{"a dead pid fails with a recorded identity", processAlive("pid"), map[string]any{"pid": dead}, &deadRecord, "not running"},
		{"an output that no longer matches the record fails", processAlive("pid"), map[string]any{"pid": self}, &otherPid, "recorded"},
		{"a missing output fails", processAlive("pid"), map[string]any{}, &identity, "resolved to nothing"},
		{"a process beside a passing action passes", withAction(processAlive("pid"), "true"), map[string]any{"pid": self}, &identity, ""},
		{"a process beside a failing action fails", withAction(processAlive("pid"), "exit 3"), map[string]any{"pid": self}, &identity, "exit status 3"},
		{"a failed process skips the action beside it", withAction(processAlive("pid"), "exit 3"), map[string]any{"pid": self}, &reused, "different process"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := RunAliveProbe(context.Background(), Probe{Action: tc.action, Self: tc.self, Process: tc.process}, SessionVars{Name: "s"})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("RunAliveProbe: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("RunAliveProbe error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func withAction(process *lang.Action, script string) *lang.Action {
	return &lang.Action{Type: lang.ActionShell, Script: script, Process: process.Process}
}

// A worker whose setup prints its pid is the shape every shipped runtime has:
// the pid is the plugin's output, and the identity is core's own record.
const spawnWorkerSetup = `sleep 60 >/dev/null 2>&1 &
echo "{\"pid\":$!}"`

func TestRunSetup_RecordsProcessIdentityAndVerifiesItOnTheNextPass(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "worker", scope: "run", setup: spawnWorkerSetup, cleanup: "true", aliveAction: processAlive("pid")}},
		[]nodeStub{{id: "worker"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	st := tasks["worker"]
	pid, ok := asPID(st.Outputs["pid"])
	if !ok {
		t.Fatalf("outputs.pid = %v, want a pid", st.Outputs["pid"])
	}
	t.Cleanup(func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() })
	if st.Process == nil || st.Process.PID != pid || st.Process.Start <= 0 {
		t.Fatalf("Process = %+v, want an identity for pid %d", st.Process, pid)
	}
	if _, leaked := st.Outputs["start"]; leaked {
		t.Errorf("the identity must not appear among the plugin's outputs: %v", st.Outputs)
	}

	execID := st.ExecutionID
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if tasks["worker"] != st || tasks["worker"].ExecutionID != execID {
		t.Fatal("a live recorded process must be skipped, not rebuilt")
	}
}

func TestRunSetup_AReusedPidIsNotAliveAndTheNodeIsRebuilt(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	marker := t.TempDir() + "/setup-ran"
	plan := buildPlan(t,
		[]taskStub{{id: "worker", scope: "run", setup: "touch " + marker + "\n" + spawnWorkerSetup, cleanup: "true", aliveAction: processAlive("pid")}},
		[]nodeStub{{id: "worker"}},
	)
	tasks := map[string]*contract.TaskState{}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	pid, _ := asPID(tasks["worker"].Outputs["pid"])
	t.Cleanup(func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() })
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// The pid is still held by a live process, but one that started at another
	// time than the recorded one: exactly what a recycled pid looks like.
	tasks["worker"].Process.Start += 1000
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("setup did not re-run for a pid held by a different process")
	}
}

func TestRunSetup_SetupWhoseProcessIsAlreadyGoneFails(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "worker", scope: "run", setup: `echo '{"pid":2147483000}'`, cleanup: "true", aliveAction: processAlive("pid")}},
		[]nodeStub{{id: "worker"}},
	)
	tasks := map[string]*contract.TaskState{}
	err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("RunSetup error = %v, want a not-running failure", err)
	}
	if st := tasks["worker"]; st == nil || st.Status != contract.TaskStatusFailed {
		t.Fatalf("worker = %+v, want a failed record", st)
	}
}

func TestExecuteTaskSetup_RecordsProcessIdentity(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t,
		[]taskStub{{id: "worker", scope: "run", setup: spawnWorkerSetup, cleanup: "true", aliveAction: processAlive("pid")}},
		[]nodeStub{{id: "worker"}},
	)
	got, err := ExecuteTaskSetup(context.Background(), plan.Run[0], nil, SessionVars{}, nil)
	if err != nil {
		t.Fatalf("ExecuteTaskSetup: %v", err)
	}
	pid, _ := asPID(got.Outputs["pid"])
	t.Cleanup(func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() })
	if got.Process == nil || got.Process.PID != pid {
		t.Fatalf("Process = %+v, want an identity for pid %d", got.Process, pid)
	}
}

// A cleanup script cannot read the identity core holds, so core answers the
// one question the script has: is the recorded process still this pid's.
func TestRunCleanup_SelfProcessAliveAnswersFromTheRecordedIdentity(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	self := os.Getpid()
	identity, err := procid.Capture(self)
	if err != nil {
		t.Fatal(err)
	}
	reused := identity
	reused.Start += 1000
	dead := exitedPid(t)

	tests := []struct {
		name    string
		outputs map[string]any
		process *contract.ProcessIdentity
		want    string
	}{
		{"the recorded process", map[string]any{"pid": self}, &identity, "true"},
		{"a reused pid", map[string]any{"pid": self}, &reused, "false"},
		{"a process that exited", map[string]any{"pid": dead}, &contract.ProcessIdentity{PID: dead, Start: identity.Start, Boot: identity.Boot}, "false"},
		{"a record without an identity", map[string]any{"pid": self}, nil, "absent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir() + "/answer"
			plan := buildPlan(t,
				[]taskStub{{
					id: "worker", scope: "run", setup: "true", alive: "true",
					cleanup: `printf '%s' "${process_alive-absent}" > "$out"`,
				}},
				[]nodeStub{{id: "worker"}},
			)
			plan.Run[0].Cleanup.Bind = map[string]*lang.Value{
				"process_alive": {Form: lang.FormFrom, From: "self.process.alive", Optional: true},
				"out":           {Form: lang.FormLiteral, Literal: out},
			}
			tasks := map[string]*contract.TaskState{
				"worker": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: tc.outputs, Process: tc.process},
			}
			if err := RunCleanup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err != nil {
				t.Fatalf("RunCleanup: %v", err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("process_alive = %q, want %q", got, tc.want)
			}
		})
	}
}
