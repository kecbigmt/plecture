package persistence

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestPutSession_ProcessIdentityRoundTripsForNodesAndTasks(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	identity := &contract.ProcessIdentity{PID: 4242, Start: 98765, Boot: "boot-a"}

	seed := &domain.Session{
		Name: "s1", CreatedAt: now, UpdatedAt: now,
		Nodes: map[string]*contract.TaskState{
			"a": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "work", Process: identity},
			"b": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "work"},
		},
		Tasks: map[string]*contract.TaskState{
			"t#1": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "work", Process: identity},
			"t#2": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, TaskID: "work"},
		},
	}
	if err := db.PutSession(ctx, seed); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	got, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	for _, tc := range []struct {
		name string
		got  *contract.TaskState
		want *contract.ProcessIdentity
	}{
		{"node with an identity", got.Nodes["a"], identity},
		{"node without one", got.Nodes["b"], nil},
		{"task with an identity", got.Tasks["t#1"], identity},
		{"task without one", got.Tasks["t#2"], nil},
	} {
		if !reflect.DeepEqual(tc.got.Process, tc.want) {
			t.Errorf("%s: Process = %+v, want %+v", tc.name, tc.got.Process, tc.want)
		}
	}

	// A later write that carries no identity clears the stored one rather
	// than leaving the earlier generation's behind.
	got.Nodes["a"].Process = nil
	if err := db.PutSession(ctx, got); err != nil {
		t.Fatalf("PutSession (clear): %v", err)
	}
	again, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Nodes["a"].Process != nil {
		t.Errorf("Process after clearing = %+v, want nil", again.Nodes["a"].Process)
	}
}
