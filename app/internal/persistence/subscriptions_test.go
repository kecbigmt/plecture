package persistence

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestSubscriptions_RuntimeSourcesAreIndependentAndIdempotent(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	mustPut(t, db, sessionWithResource("one", caseResource("case://1")))
	resource := *caseResource("case://1")
	for i := 0; i < 2; i++ {
		if err := db.AddSubscription(ctx, "one", &resource, "runtime", nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListSubscriptions(ctx, "one", "")
	if err != nil || len(rows) != 2 || rows[0].Source != "dispatch" || rows[1].Source != "runtime" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
	if err := db.RemoveSubscription(ctx, "one", "case://1", "runtime"); err != nil {
		t.Fatal(err)
	}
	rows, err = db.ListSubscriptions(ctx, "one", "")
	if err != nil || len(rows) != 1 || rows[0].Source != "dispatch" {
		t.Fatalf("rows after runtime unsubscribe = %+v, err = %v", rows, err)
	}
	if err := db.DestroySession(ctx, "one", time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err = db.ListSubscriptions(ctx, "one", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows after destroy = %+v, err = %v", rows, err)
	}
}

func TestSubscriptions_TaskAndNodeRowsFollowLiveBindings(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	s := sessionWithResource("one", nil)
	s.Tasks = map[string]*contract.TaskState{
		"first":  {TaskID: "work", Scope: "run", Status: contract.TaskStatusProduced, Resource: caseResource("case://1")},
		"second": {TaskID: "work", Scope: "run", Status: contract.TaskStatusProduced, Resource: caseResource("case://1")},
	}
	s.Nodes = map[string]*contract.TaskState{
		"watch": {TaskID: "watch", Scope: "session", Status: contract.TaskStatusProduced, Subscription: caseResource("case://1")},
	}
	mustPut(t, db, s)
	assertSources := func(want ...string) {
		t.Helper()
		rows, err := db.ListSubscriptions(ctx, "one", "")
		if err != nil || len(rows) != len(want) {
			t.Fatalf("rows = %+v, err = %v, want sources %v", rows, err, want)
		}
		for i, row := range rows {
			if row.Source != want[i] {
				t.Fatalf("sources = %+v, want %v", rows, want)
			}
		}
	}
	assertSources("node", "task")
	mustUpdate(t, db, "one", func(s *domain.Session) { delete(s.Tasks, "first") })
	assertSources("node", "task")
	mustUpdate(t, db, "one", func(s *domain.Session) { s.Tasks = nil })
	assertSources("node")
	mustUpdate(t, db, "one", func(s *domain.Session) { s.Nodes["watch"].Status = contract.TaskStatusCleaned })
	assertSources()
}

func TestSubscriptions_LimitCountsDistinctSessions(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	mustPut(t, db, sessionWithResource("one", caseResource("case://1")))
	mustPut(t, db, sessionWithResource("two", nil))
	resource := *caseResource("case://1")
	if err := db.AddSubscription(ctx, "one", &resource, "runtime", nil, 1); err != nil {
		t.Fatalf("same session should use no second slot: %v", err)
	}
	if err := db.AddSubscription(ctx, "two", &resource, "runtime", nil, 1); err == nil {
		t.Fatal("second session should exceed max_subscribers")
	}
	rows, err := db.ListSubscriptions(ctx, "two", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected session rows = %+v, err = %v", rows, err)
	}
}

func TestSubscriptions_DispatchAndTaskAdmissionRejectAtLimitWithoutWritingBindings(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	limited := func() *contract.Resource {
		return &contract.Resource{Type: "case", Name: "case-1", Locator: "case://1", MaxSubscribers: 1, MaxSubscribersDeclared: true}
	}
	mustPut(t, db, sessionWithResource("one", limited()))
	if err := db.PutSession(ctx, sessionWithResource("two", limited())); err == nil {
		t.Fatal("second dispatch should exceed the subscriber limit")
	}
	if got, err := db.GetSession(ctx, "two"); err != nil || got != nil {
		t.Fatalf("rejected dispatch wrote session %+v, err = %v", got, err)
	}
	mustPut(t, db, sessionWithResource("three", nil))
	err := db.UpdateSession(ctx, "three", func(s *domain.Session) error {
		s.Tasks = map[string]*contract.TaskState{"work": {TaskID: "work", Scope: "run", Status: contract.TaskStatusProduced, Resource: limited()}}
		return nil
	})
	if err == nil {
		t.Fatal("task admission should exceed the subscriber limit")
	}
	got, err := db.GetSession(ctx, "three")
	if err != nil || len(got.Tasks) != 0 {
		t.Fatalf("rejected task binding = %+v, err = %v", got, err)
	}
}

func TestSubscriptions_EntryBindingClearsRemovedDeclarationLimit(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	limited := &contract.Resource{Type: "case", Name: "case-1", Locator: "case://1", MaxSubscribers: 1, MaxSubscribersDeclared: true}
	mustPut(t, db, sessionWithResource("one", limited))
	unlimited := &contract.Resource{Type: "case", Name: "case-1", Locator: "case://1", MaxSubscribersDeclared: true}
	if err := db.PutSession(ctx, sessionWithResource("two", unlimited)); err != nil {
		t.Fatalf("unlimited declaration must admit the second session: %v", err)
	}
	var limit sql.NullInt64
	if err := db.write.QueryRowContext(ctx, `SELECT max_subscribers FROM resources WHERE name = 'case-1'`).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit.Valid {
		t.Fatalf("stored cap = %d, want NULL after declaration removes it", limit.Int64)
	}
}
