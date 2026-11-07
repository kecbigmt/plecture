package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func testResource(name, locator string) *contract.Resource {
	return &contract.Resource{Type: "t", Name: name, Locator: locator}
}

func caseResource(locator string) *contract.Resource {
	return &contract.Resource{Type: "case", Name: "case-1", Locator: locator}
}

func sessionWithResource(name string, r *contract.Resource) *domain.Session {
	now := time.Now().UTC()
	return &domain.Session{Name: name, Workflow: "wf", Resource: r, CreatedAt: now, UpdatedAt: now}
}

func countResources(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.write.QueryRow(`SELECT COUNT(*) FROM resources`).Scan(&n); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	return n
}

func TestPutSession_TwoSpellingsDerivingOneNameShareOneResourceRow(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	if err := db.PutSession(ctx, sessionWithResource("s1", caseResource("case://1"))); err != nil {
		t.Fatalf("put s1: %v", err)
	}
	if err := db.PutSession(ctx, sessionWithResource("s2", caseResource("case://1?reply=2"))); err != nil {
		t.Fatalf("put s2: %v", err)
	}

	first, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.GetSession(ctx, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if first.Resource == nil || second.Resource == nil || first.Resource.ID != second.Resource.ID {
		t.Fatalf("resources = %+v and %+v, want one row", first.Resource, second.Resource)
	}
	if second.Resource.Locator != "case://1" {
		t.Errorf("locator = %q, want the first spelling case://1", second.Resource.Locator)
	}
	if n := countResources(t, db); n != 1 {
		t.Errorf("resources rows = %d, want 1", n)
	}
}

func TestPutSession_ANameRecordedForAnotherTypeIsRefused(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	if err := db.PutSession(ctx, sessionWithResource("s1", caseResource("case://1"))); err != nil {
		t.Fatalf("put s1: %v", err)
	}

	err := db.PutSession(ctx, sessionWithResource("s2", &contract.Resource{Type: "alt", Name: "case-1", Locator: "alt://1"}))
	var conflict *ResourceTypeConflictError
	if !errors.As(err, &conflict) || conflict.Recorded != "case" || conflict.Requested != "alt" {
		t.Fatalf("err = %v, want a type conflict between case and alt", err)
	}
	if got, _ := db.GetSession(ctx, "s2"); got != nil {
		t.Errorf("refused session was written: %+v", got)
	}
}

func TestResources_ARowIsRemovedOnceNothingReferencesIt(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		seed    func(t *testing.T, db *DB)
		release func(t *testing.T, db *DB)
	}{
		{
			name: "session purged",
			seed: func(t *testing.T, db *DB) {
				mustPut(t, db, sessionWithResource("s1", caseResource("case://1")))
			},
			release: func(t *testing.T, db *DB) {
				if err := db.PurgeSessionByName(ctx, "s1"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "task instance removed",
			seed: func(t *testing.T, db *DB) {
				s := sessionWithResource("s1", nil)
				s.Tasks = map[string]*contract.TaskState{"t#1": {Scope: "run", Status: contract.TaskStatusProduced, TaskID: "t", Resource: caseResource("case://1")}}
				mustPut(t, db, s)
			},
			release: func(t *testing.T, db *DB) {
				mustUpdate(t, db, "s1", func(s *domain.Session) { s.Tasks = nil })
			},
		},
		{
			name: "released node pruned",
			seed: func(t *testing.T, db *DB) {
				s := sessionWithResource("s1", nil)
				s.Nodes = map[string]*contract.TaskState{"n": {Scope: "session", Status: contract.TaskStatusCleaned, Resource: caseResource("case://1")}}
				mustPut(t, db, s)
			},
			release: func(t *testing.T, db *DB) {
				mustUpdate(t, db, "s1", func(s *domain.Session) { s.Nodes = nil })
			},
		},
		{
			name: "node subscription released",
			seed: func(t *testing.T, db *DB) {
				s := sessionWithResource("s1", nil)
				s.Nodes = map[string]*contract.TaskState{"n": {Scope: "session", Status: contract.TaskStatusCleaned, Subscription: caseResource("case://1")}}
				mustPut(t, db, s)
			},
			release: func(t *testing.T, db *DB) {
				mustUpdate(t, db, "s1", func(s *domain.Session) { s.Nodes = nil })
			},
		},
		{
			name: "population member dropped",
			seed: func(t *testing.T, db *DB) {
				mustUpdatePopulation(t, db, func(p *domain.PopulationState) {
					p.Members["case-1"] = &domain.PopulationMember{Resource: *caseResource("case://1"), Generation: 1}
				})
			},
			release: func(t *testing.T, db *DB) {
				mustUpdatePopulation(t, db, func(p *domain.PopulationState) { delete(p.Members, "case-1") })
			},
		},
		{
			name: "subscription retry deleted",
			seed: func(t *testing.T, db *DB) {
				mustPut(t, db, sessionWithResource("s1", nil))
				if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetrySubscribe, *caseResource("case://1")); err != nil {
					t.Fatal(err)
				}
			},
			release: func(t *testing.T, db *DB) {
				retries, err := db.SubscriptionRetriesForLiveSession(ctx, "s1")
				if err != nil || len(retries) != 1 {
					t.Fatalf("retries = %v, %v", retries, err)
				}
				if err := db.DeleteSubscriptionRetryForLiveSession(ctx, "s1", subscriptionRetrySubscribe, retries[0].Resource.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := migratedTestDB(t)
			tt.seed(t, db)
			if n := countResources(t, db); n != 1 {
				t.Fatalf("resources rows after seeding = %d, want 1", n)
			}
			tt.release(t, db)
			if n := countResources(t, db); n != 0 {
				t.Errorf("resources rows = %d, want 0 once nothing references the row", n)
			}
		})
	}
}

func TestResources_ARowAnotherTableStillReferencesSurvives(t *testing.T) {
	db := migratedTestDB(t)
	s := sessionWithResource("s1", caseResource("case://1"))
	s.Tasks = map[string]*contract.TaskState{"t#1": {Scope: "run", Status: contract.TaskStatusProduced, TaskID: "t", Resource: caseResource("case://1")}}
	mustPut(t, db, s)

	mustUpdate(t, db, "s1", func(s *domain.Session) { s.Tasks = nil })

	if n := countResources(t, db); n != 1 {
		t.Errorf("resources rows = %d, want the session's row kept", n)
	}
}

func TestUpdatePopulation_RecordsMembersAgainstTheirResource(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	mustUpdatePopulation(t, db, func(p *domain.PopulationState) {
		p.Members["case-1"] = &domain.PopulationMember{Resource: *caseResource("case://1"), Generation: 1, LastBlockers: []string{"busy"}}
	})
	mustUpdatePopulation(t, db, func(p *domain.PopulationState) {
		p.Members["case-1"].Item = map[string]any{"resource": "case://1?reply=2"}
	})

	got, err := db.Population(ctx, contract.PopulationProvenance{Workflow: "wf", Name: "pop"})
	if err != nil {
		t.Fatal(err)
	}
	member := got.Members["case-1"]
	if member == nil || member.Resource.Type != "case" || member.Resource.Locator != "case://1" || member.Resource.ID == "" {
		t.Fatalf("member = %+v, want case-1 recorded once with its first locator", member)
	}
	if len(member.LastBlockers) != 1 || member.LastBlockers[0] != "busy" {
		t.Errorf("blockers = %v, want [busy]", member.LastBlockers)
	}
	if n := countResources(t, db); n != 1 {
		t.Errorf("resources rows = %d, want 1", n)
	}
}

func mustPut(t *testing.T, db *DB, s *domain.Session) {
	t.Helper()
	if err := db.PutSession(context.Background(), s); err != nil {
		t.Fatalf("put %s: %v", s.Name, err)
	}
}

func mustUpdate(t *testing.T, db *DB, name string, fn func(*domain.Session)) {
	t.Helper()
	if err := db.UpdateSession(context.Background(), name, func(s *domain.Session) error { fn(s); return nil }); err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
}

func mustUpdatePopulation(t *testing.T, db *DB, fn func(*domain.PopulationState)) {
	t.Helper()
	if err := db.UpdatePopulation(context.Background(), contract.PopulationProvenance{Workflow: "wf", Name: "pop"}, func(p *domain.PopulationState) error { fn(p); return nil }); err != nil {
		t.Fatalf("update population: %v", err)
	}
}

func TestResources_APendingUnsubscribeRetryKeepsTheRowAfterItsBindingIsGone(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	s := sessionWithResource("s1", nil)
	s.Tasks = map[string]*contract.TaskState{"t#1": {Scope: "run", Status: contract.TaskStatusProduced, TaskID: "t", Resource: caseResource("case://1")}}
	mustPut(t, db, s)
	if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetryUnsubscribe, *caseResource("case://1")); err != nil {
		t.Fatal(err)
	}

	mustUpdate(t, db, "s1", func(s *domain.Session) { s.Tasks = nil })
	if n := countResources(t, db); n != 1 {
		t.Fatalf("resources rows = %d while the unsubscribe retry is pending, want 1", n)
	}
	retries, err := db.SubscriptionRetriesForLiveSession(ctx, "s1")
	if err != nil || len(retries) != 1 || retries[0].Resource.Locator != "case://1" {
		t.Fatalf("retries = %+v, %v; want the pending unsubscribe still naming case://1", retries, err)
	}

	if err := db.DeleteSubscriptionRetry(ctx, retries[0].SessionID, retries[0].Action, retries[0].Resource.ID); err != nil {
		t.Fatal(err)
	}
	if n := countResources(t, db); n != 0 {
		t.Errorf("resources rows = %d once the retry is gone, want 0", n)
	}
}

func TestQueueSubscriptionRetry_AdoptsTheRowAnotherBindingRecordedAfterItsOwnWasReleased(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	s1 := sessionWithResource("s1", nil)
	s1.Tasks = map[string]*contract.TaskState{"t#1": {Scope: "run", Status: contract.TaskStatusProduced, TaskID: "t", Resource: caseResource("case://1")}}
	mustPut(t, db, s1)
	stored, err := db.GetSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	retained := *stored.Tasks["t#1"].Resource

	mustUpdate(t, db, "s1", func(s *domain.Session) { s.Tasks = nil })
	mustPut(t, db, sessionWithResource("s2", caseResource("case://1?reply=2")))

	if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetryUnsubscribe, retained); err != nil {
		t.Fatalf("queue unsubscribe with the released row's id: %v", err)
	}
	retries, err := db.SubscriptionRetriesForLiveSession(ctx, "s1")
	if err != nil || len(retries) != 1 {
		t.Fatalf("retries = %+v, %v; want the pending unsubscribe", retries, err)
	}
	s2, err := db.GetSession(ctx, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if retries[0].Resource.ID != s2.Resource.ID {
		t.Errorf("retry references %s, want the row now recorded under the name (%s)", retries[0].Resource.ID, s2.Resource.ID)
	}
}
