package persistence

import (
	"context"
	"errors"
	"fmt"
	contract "github.com/kecbigmt/plecture/contracts/state"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

var errBoom = errors.New("boom")

func TestPopulation_MissingReturnsNilNil(t *testing.T) {
	db := migratedTestDB(t)
	got, err := db.Population(context.Background(), contract.PopulationProvenance{Workflow: "nonexistent"})
	if err != nil {
		t.Fatalf("Population: %v", err)
	}
	if got != nil {
		t.Errorf("Population = %v, want nil", got)
	}
}

func TestUpdatePopulation_CreatesAndPersistsMembers(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
		p.Workflow = "wf"
		p.Name = "pop"
		p.Members["res-1"] = &domain.PopulationMember{Resource: *testResource("res-1", "t://res-1"), Generation: 1, PendingUp: true, Item: map[string]any{"k": "v"}}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdatePopulation: %v", err)
	}

	got, err := db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatalf("Population: %v", err)
	}
	if got == nil {
		t.Fatal("Population returned nil after UpdatePopulation")
	}
	if got.Workflow != "wf" || got.Name != "pop" {
		t.Errorf("population = %+v", got)
	}
	member := got.Members["res-1"]
	if member == nil {
		t.Fatal("member res-1 missing")
	}
	if member.Generation != 1 || !member.PendingUp {
		t.Errorf("member = %+v", member)
	}
	if member.Item["k"] != "v" {
		t.Errorf("member.Item = %v", member.Item)
	}
}

func TestUpdatePopulation_ReplacesMembersRatherThanAccumulating(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	mutate := func(fn func(*domain.PopulationState)) {
		if err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
			fn(p)
			return nil
		}); err != nil {
			t.Fatalf("UpdatePopulation: %v", err)
		}
	}

	mutate(func(p *domain.PopulationState) {
		p.Members["a"] = &domain.PopulationMember{Resource: *testResource("a", "t://a"), Generation: 1}
	})
	mutate(func(p *domain.PopulationState) {
		delete(p.Members, "a")
		p.Members["b"] = &domain.PopulationMember{Resource: *testResource("b", "t://b"), Generation: 1}
	})

	got, err := db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatalf("Population: %v", err)
	}
	if _, ok := got.Members["a"]; ok {
		t.Error("member a survived after being removed from the callback's map")
	}
	if _, ok := got.Members["b"]; !ok {
		t.Error("member b missing")
	}
}

func TestUpdatePopulation_FnErrorAbortsWithoutWriting(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	wantErr := errBoom
	err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
		p.Workflow = "should-not-persist"
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("UpdatePopulation error = %v, want %v", err, wantErr)
	}

	got, err := db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatalf("Population: %v", err)
	}
	if got != nil {
		t.Errorf("Population = %+v, want nil (fn's error must have prevented any write)", got)
	}
}

func TestUpdatePopulation_MembersMapIsNeverNilInsideFn(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	var sawNilMembers bool
	if err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
		if p.Members == nil {
			sawNilMembers = true
		}
		return nil
	}); err != nil {
		t.Fatalf("UpdatePopulation: %v", err)
	}
	if sawNilMembers {
		t.Error("fn saw a nil Members map; callers index it directly and would panic")
	}
}

// TestUpdatePopulation_LastBlockersRoundTripsThroughChildTableInOrder proves
// LastBlockers survives the population_member_blockers split, in the order
// recorded (the table's position column, not the row's own insert order).
func TestUpdatePopulation_LastBlockersRoundTripsThroughChildTableInOrder(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	if err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
		p.Members["r1"] = &domain.PopulationMember{Resource: *testResource("r1", "t://r1"), LastBlockers: []string{"open PR", "failing check"}}
		return nil
	}); err != nil {
		t.Fatalf("UpdatePopulation: %v", err)
	}

	got, err := db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatalf("Population: %v", err)
	}
	member := got.Members["r1"]
	if member == nil {
		t.Fatal("member r1 missing")
	}
	want := []string{"open PR", "failing check"}
	if len(member.LastBlockers) != len(want) {
		t.Fatalf("LastBlockers = %v, want %v", member.LastBlockers, want)
	}
	for i := range want {
		if member.LastBlockers[i] != want[i] {
			t.Errorf("LastBlockers[%d] = %q, want %q", i, member.LastBlockers[i], want[i])
		}
	}

	// A later evaluation with fewer blockers replaces the list rather than
	// accumulating it.
	if err := db.UpdatePopulation(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error {
		p.Members["r1"].LastBlockers = []string{"open PR"}
		return nil
	}); err != nil {
		t.Fatalf("UpdatePopulation (replace): %v", err)
	}
	got, err = db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatalf("Population (after replace): %v", err)
	}
	if fmt.Sprint(got.Members["r1"].LastBlockers) != fmt.Sprint([]string{"open PR"}) {
		t.Fatalf("LastBlockers after replace = %v, want [open PR]", got.Members["r1"].LastBlockers)
	}
}

func TestUpdatePopulation_LastDecisionRoundTripsThroughDecisionKindAndReason(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name         string
		lastDecision string
	}{
		{"empty", ""},
		{"bare kind", "plect.workflow_population.destroy"},
		{"kind with reason", "plect.workflow_population.destroy_deferred:blocked on open PR"},
		{"reason containing a colon", "plect.workflow_population.destroy:blocked: awaiting review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := contract.PopulationProvenance{Workflow: "wf", Name: tc.name}
			if err := db.UpdatePopulation(ctx, key, func(p *domain.PopulationState) error {
				p.Members["r1"] = &domain.PopulationMember{Resource: *testResource("r1", "t://r1"), LastDecision: tc.lastDecision}
				return nil
			}); err != nil {
				t.Fatalf("UpdatePopulation: %v", err)
			}

			got, err := db.Population(ctx, key)
			if err != nil {
				t.Fatalf("Population: %v", err)
			}
			member := got.Members["r1"]
			if member == nil {
				t.Fatal("member r1 missing")
			}
			if member.LastDecision != tc.lastDecision {
				t.Errorf("LastDecision = %q, want %q", member.LastDecision, tc.lastDecision)
			}
		})
	}
}

func TestUpdatePopulation_SameNameInAnotherContextIsAnotherPopulation(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	widgets := contract.PopulationProvenance{ProjectRoot: "/projects/widgets", Workflow: "review", Name: "standing"}
	global := contract.PopulationProvenance{Workflow: "review", Name: "standing"}
	for key, resource := range map[contract.PopulationProvenance]string{widgets: "res-w", global: "res-g"} {
		if err := db.UpdatePopulation(ctx, key, func(p *domain.PopulationState) error {
			p.Members[resource] = &domain.PopulationMember{Resource: *testResource(resource, "t://"+resource)}
			return nil
		}); err != nil {
			t.Fatalf("UpdatePopulation(%+v): %v", key, err)
		}
	}

	for key, want := range map[contract.PopulationProvenance]string{widgets: "res-w", global: "res-g"} {
		got, err := db.Population(ctx, key)
		if err != nil || got == nil {
			t.Fatalf("Population(%+v) = %v, %v", key, got, err)
		}
		if len(got.Members) != 1 || got.Members[want] == nil || got.ProjectRoot != key.ProjectRoot {
			t.Errorf("Population(%+v) = root %q members %v, want only %q", key, got.ProjectRoot, got.Members, want)
		}
	}
}
