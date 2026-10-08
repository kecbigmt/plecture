package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func openMigratedAt(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// shadowedFixture reproduces the stored shape a build that persisted a failed
// setup attempt at sequence 0 left behind: node "a" has a cleaned execution at
// sequence 17 and, below it, an unreleased failed one carrying outputs.
func shadowedFixture(t *testing.T, db *DB, extra map[string]*contract.TaskState) (cleanedID, failedID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	put := func(nodes map[string]*contract.TaskState) {
		t.Helper()
		if err := db.PutSession(ctx, &domain.Session{Name: "s1", CreatedAt: now, UpdatedAt: now, Nodes: nodes, Tasks: extra}); err != nil {
			t.Fatalf("PutSession: %v", err)
		}
	}
	put(map[string]*contract.TaskState{"a": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 17, Outputs: map[string]any{"gen": "old"}}})
	cleanedID = nodeExecutionIDForTest(t, db, "s1", "a")
	put(map[string]*contract.TaskState{"a": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusCleaned, Seq: 17, ExecutionID: cleanedID, Outputs: map[string]any{"gen": "old"}}})
	put(map[string]*contract.TaskState{"a": {
		Scope: contract.TaskScopeRun, Status: contract.TaskStatusFailed, NewExecution: true, Error: "boom",
		Outputs: map[string]any{"gen": "old"},
	}})
	if got := nodeExecutionIDForTest(t, db, "s1", "a"); got != cleanedID {
		t.Fatalf("fixture: current execution is %q, want the cleaned %q hiding the failed one", got, cleanedID)
	}
	var id string
	if err := db.write.QueryRowContext(context.Background(), `SELECT id FROM node_executions WHERE status = 'failed'`).Scan(&id); err != nil {
		t.Fatalf("fixture: find failed row: %v", err)
	}
	return cleanedID, id
}

func TestRepairShadowedNodeExecutions_ListsWithoutWriting(t *testing.T) {
	path := PathIn(t.TempDir())
	db := openMigratedAt(t, path)
	_, failedID := shadowedFixture(t, db, nil)

	got, err := ShadowedNodeExecutions(context.Background(), path)
	if err != nil {
		t.Fatalf("ShadowedNodeExecutions: %v", err)
	}
	if len(got) != 1 || got[0].ExecutionID != failedID || got[0].NodeID != "a" || got[0].SessionName != "s1" || got[0].Sequence != 0 {
		t.Fatalf("shadowed = %+v, want exactly the failed execution %q of s1/a at sequence 0", got, failedID)
	}
	if again, _ := ShadowedNodeExecutions(context.Background(), path); len(again) != 1 {
		t.Fatalf("listing changed the database: second listing = %+v", again)
	}
}

func TestRepairShadowedNodeExecutions_MakesTheUnreleasedExecutionCurrentAndKeepsHistory(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	cleanedID, failedID := shadowedFixture(t, db, nil)

	repaired, err := db.RepairShadowedNodeExecutions(ctx)
	if err != nil {
		t.Fatalf("RepairShadowedNodeExecutions: %v", err)
	}
	if len(repaired) != 1 || repaired[0].ExecutionID != failedID || repaired[0].NewSequence <= 17 {
		t.Fatalf("repaired = %+v, want the failed execution moved above sequence 17", repaired)
	}

	if got := nodeExecutionIDForTest(t, db, "s1", "a"); got != failedID {
		t.Fatalf("current execution = %q, want the repaired failed one %q", got, failedID)
	}
	if got := countNodeExecutionsForTest(t, db, "s1", "a"); got != 2 {
		t.Fatalf("node_executions rows = %d, want both generations retained", got)
	}
	session, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	node := session.Nodes["a"]
	if node.Status != contract.TaskStatusFailed || node.Error != "boom" || node.Outputs["gen"] != "old" {
		t.Fatalf("node after repair = %+v, want the failed attempt with its error and outputs intact", node)
	}
	var cleanedOutputs string
	if err := db.write.QueryRowContext(ctx, `SELECT outputs_json FROM node_executions WHERE id = ?`, cleanedID).Scan(&cleanedOutputs); err != nil || cleanedOutputs == "" {
		t.Fatalf("cleaned execution's outputs = %q, %v; want them kept", cleanedOutputs, err)
	}

	if again, err := db.RepairShadowedNodeExecutions(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second repair = %+v, %v; want a no-op", again, err)
	}
}

func TestRepairShadowedNodeExecutions_RepairedExecutionAcceptsTheRetry(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	shadowedFixture(t, db, nil)
	if _, err := db.RepairShadowedNodeExecutions(ctx); err != nil {
		t.Fatalf("RepairShadowedNodeExecutions: %v", err)
	}
	session, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	node := session.Nodes["a"]
	node.Status = contract.TaskStatusProduced
	if err := db.PutSession(ctx, session); err != nil {
		t.Fatalf("PutSession (retry continuing the repaired execution): %v", err)
	}
}

func TestRepairShadowedNodeExecutions_NewSequenceClearsTheSessionsTaskInstances(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	shadowedFixture(t, db, map[string]*contract.TaskState{
		"inst": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, TaskID: "work", Seq: 40},
	})

	repaired, err := db.RepairShadowedNodeExecutions(ctx)
	if err != nil {
		t.Fatalf("RepairShadowedNodeExecutions: %v", err)
	}
	if len(repaired) != 1 || repaired[0].NewSequence <= 40 {
		t.Fatalf("repaired = %+v, want a sequence above the task instance's 40", repaired)
	}
}

func TestRepairShadowedNodeExecutions_LeavesAHealthyDatabaseAlone(t *testing.T) {
	path := PathIn(t.TempDir())
	db := openMigratedAt(t, path)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.PutSession(ctx, &domain.Session{Name: "s1", CreatedAt: now, UpdatedAt: now, Nodes: map[string]*contract.TaskState{
		"a": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Seq: 3},
	}}); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if got, err := db.RepairShadowedNodeExecutions(ctx); err != nil || len(got) != 0 {
		t.Fatalf("repair on a healthy database = %+v, %v; want nothing", got, err)
	}
	if got, err := ShadowedNodeExecutions(ctx, path); err != nil || len(got) != 0 {
		t.Fatalf("listing on a healthy database = %+v, %v; want nothing", got, err)
	}
}
