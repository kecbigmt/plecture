package task

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/persistence"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const staleCleanedSeq = 17

func TestRunSetup_FailedNewExecutionOutranksTheCleanedOneItReplaces(t *testing.T) {
	cases := []struct {
		name string
		defs []taskStub
	}{
		{"effect exits non-zero", []taskStub{{id: "a", scope: "run", setup: "exit 1"}}},
		{"stdout is not the outputs contract", []taskStub{{id: "a", scope: "run", setup: "echo not-json"}}},
		{"outputs violate the schema", []taskStub{{
			id: "a", scope: "run", setup: `echo '{"v":1}'`,
			outputsSchema: map[string]any{"type": "object", "required": []any{"missing"}},
		}}},
		{"inputs violate the schema", []taskStub{{
			id: "a", scope: "run", setup: "echo '{}'",
			inputsSchema: map[string]any{"type": "object", "required": []any{"missing"}},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("bash"); err != nil {
				t.Skip("bash not available")
			}
			plan := buildPlan(t, tc.defs, []nodeStub{{id: "a"}})
			tasks := map[string]*contract.TaskState{
				"a": {Scope: "run", Status: contract.TaskStatusCleaned, ExecutionID: "old", Seq: staleCleanedSeq},
			}
			if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
				t.Fatal("RunSetup: want a setup failure, got nil")
			}
			got := tasks["a"]
			if got.Status != contract.TaskStatusFailed {
				t.Fatalf("Status = %q, want failed", got.Status)
			}
			if got.Seq <= staleCleanedSeq {
				t.Errorf("Seq = %d, want greater than the cleaned execution's %d", got.Seq, staleCleanedSeq)
			}
			if got.ExecutionID != "" || !got.NewExecution {
				t.Errorf("ExecutionID = %q, NewExecution = %v, want a fresh execution", got.ExecutionID, got.NewExecution)
			}
		})
	}
}

func TestRunSetup_FailedRetryKeepsTheUnreleasedExecutionIdentityAndSeq(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	plan := buildPlan(t, []taskStub{{id: "a", scope: "run", setup: "exit 1"}}, []nodeStub{{id: "a"}})
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusFailed, ExecutionID: "live", Seq: 5},
	}
	if err := RunSetup(context.Background(), plan.Run, SessionVars{}, tasks, nil); err == nil {
		t.Fatal("RunSetup: want a setup failure, got nil")
	}
	got := tasks["a"]
	if got.ExecutionID != "live" || got.NewExecution || got.Seq != 5 {
		t.Fatalf("ExecutionID = %q, NewExecution = %v, Seq = %d; want live, false, 5", got.ExecutionID, got.NewExecution, got.Seq)
	}
}

// sequenceFixture persists a node whose previous execution was cleaned at
// staleCleanedSeq, then reopens the database so every later step reads what a
// fresh process would.
type sequenceFixture struct {
	t    *testing.T
	path string
	db   *persistence.DB
	now  time.Time
}

func newSequenceFixture(t *testing.T) *sequenceFixture {
	t.Helper()
	f := &sequenceFixture{t: t, path: persistence.PathIn(t.TempDir()), now: time.Now().UTC()}
	f.reopen()
	f.put(map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Seq: staleCleanedSeq},
	})
	old := f.load().Nodes["a"]
	f.put(map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusCleaned, ExecutionID: old.ExecutionID, Seq: staleCleanedSeq},
	})
	f.reopen()
	return f
}

func (f *sequenceFixture) reopen() {
	f.t.Helper()
	if f.db != nil {
		if err := f.db.Close(); err != nil {
			f.t.Fatalf("Close: %v", err)
		}
	}
	db, err := persistence.Open(f.path)
	if err != nil {
		f.t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		f.t.Fatalf("Migrate: %v", err)
	}
	f.t.Cleanup(func() { db.Close() })
	f.db = db
}

func (f *sequenceFixture) put(nodes map[string]*contract.TaskState) {
	f.t.Helper()
	s := &domain.Session{Name: "s1", CreatedAt: f.now, UpdatedAt: f.now, Nodes: nodes}
	if err := f.db.PutSession(context.Background(), s); err != nil {
		f.t.Fatalf("PutSession: %v", err)
	}
}

func (f *sequenceFixture) load() *domain.Session {
	f.t.Helper()
	s, err := f.db.GetSession(context.Background(), "s1")
	if err != nil {
		f.t.Fatalf("GetSession: %v", err)
	}
	return s
}

// setupAndPersist runs one load -> RunSetup -> save cycle the way a command
// does, against whatever the database currently says, and returns RunSetup's
// error.
func (f *sequenceFixture) setupAndPersist(plan *Plan) error {
	f.t.Helper()
	session := f.load()
	merged := domain.MergedTasks(session)
	setupErr := RunSetup(context.Background(), plan.Run, SessionVars{}, merged, nil)
	f.put(map[string]*contract.TaskState{"a": merged["a"]})
	return setupErr
}

func TestRunSetup_RetryAfterFailedSetupOverCleanedExecution(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	marker := filepath.Join(t.TempDir(), "ready")
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "test -f " + marker + " && echo '{\"v\":\"ok\"}'"}},
		[]nodeStub{{id: "a"}},
	)

	t.Run("retry that succeeds", func(t *testing.T) {
		os.Remove(marker)
		f := newSequenceFixture(t)
		if err := f.setupAndPersist(plan); err == nil {
			t.Fatal("first setup: want a failure, got nil")
		}
		f.reopen()
		failed := f.load().Nodes["a"]
		if failed.Status != contract.TaskStatusFailed {
			t.Fatalf("after the failed setup the node reads as %q (seq %d), want failed", failed.Status, failed.Seq)
		}

		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := f.setupAndPersist(plan); err != nil {
			t.Fatalf("retry: %v", err)
		}
		f.reopen()
		got := f.load().Nodes["a"]
		if got.Status != contract.TaskStatusProduced || got.ExecutionID != failed.ExecutionID {
			t.Fatalf("after the retry the node is %q on execution %q, want produced on the failed attempt's %q", got.Status, got.ExecutionID, failed.ExecutionID)
		}
	})

	t.Run("retry that fails again", func(t *testing.T) {
		os.Remove(marker)
		f := newSequenceFixture(t)
		if err := f.setupAndPersist(plan); err == nil {
			t.Fatal("first setup: want a failure, got nil")
		}
		f.reopen()
		firstFailure := f.load().Nodes["a"]

		if err := f.setupAndPersist(plan); err == nil {
			t.Fatal("retry: want a failure, got nil")
		}
		f.reopen()
		got := f.load().Nodes["a"]
		if got.Status != contract.TaskStatusFailed || got.ExecutionID != firstFailure.ExecutionID || got.Seq != firstFailure.Seq {
			t.Fatalf("after the second failure the node is %q on %q seq %d, want failed on %q seq %d",
				got.Status, got.ExecutionID, got.Seq, firstFailure.ExecutionID, firstFailure.Seq)
		}
	})
}

func TestRunWorkflowSetup_FailureOutranksTheCleanedExecutionItReplaces(t *testing.T) {
	prov := config.WorkspaceProviderConfig{ID: "wf", Setup: providerExec(`echo '{"branch":"b"}'`)}
	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {Scope: contract.TaskScopeSession, Status: contract.TaskStatusCleaned, ExecutionID: "old", Seq: staleCleanedSeq},
	}
	if _, err := RunWorkflowSetup(prov, effect.WorkflowHookVars{}, tasks, nil); err == nil {
		t.Fatal("RunWorkflowSetup: want a setup failure, got nil")
	}
	got := tasks[contract.WorkflowPseudoNodeID]
	if got.Status != contract.TaskStatusFailed || got.Seq <= staleCleanedSeq {
		t.Fatalf("Status = %q, Seq = %d, want failed with a Seq above the cleaned execution's %d", got.Status, got.Seq, staleCleanedSeq)
	}
}
