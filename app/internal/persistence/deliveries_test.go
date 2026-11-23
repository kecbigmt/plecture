package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func recordDeliveryForTest(t *testing.T, db *DB, session, eventID string, at, staleBefore time.Time) {
	t.Helper()
	d := domain.UnansweredDelivery{EventID: eventID, EventType: "user.emit", DeliveredAt: at}
	if err := db.RecordUnansweredDelivery(context.Background(), session, d, staleBefore); err != nil {
		t.Fatalf("record %s: %v", eventID, err)
	}
}

func unansweredDeliveryForTest(t *testing.T, db *DB, session string) *domain.UnansweredDelivery {
	t.Helper()
	d, err := db.UnansweredDelivery(context.Background(), session)
	if err != nil {
		t.Fatalf("read unanswered delivery: %v", err)
	}
	return d
}

func TestRecordUnansweredDelivery_RoundTripsTheRecord(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")
	at := time.Now().UTC().Truncate(time.Microsecond)

	if got := unansweredDeliveryForTest(t, db, "s1"); got != nil {
		t.Fatalf("before any delivery = %+v, want none", got)
	}
	recordDeliveryForTest(t, db, "s1", "e1", at, time.Time{})

	got := unansweredDeliveryForTest(t, db, "s1")
	if got == nil || got.EventID != "e1" || got.EventType != "user.emit" || !got.DeliveredAt.Equal(at) {
		t.Fatalf("record = %+v, want e1 user.emit at %s", got, at)
	}
}

func TestRecordUnansweredDelivery_KeepsTheEarliestUnansweredDelivery(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")
	first := time.Now().UTC().Add(-time.Minute)

	recordDeliveryForTest(t, db, "s1", "e1", first, time.Time{})
	recordDeliveryForTest(t, db, "s1", "e2", first.Add(30*time.Second), time.Time{})

	if got := unansweredDeliveryForTest(t, db, "s1"); got == nil || got.EventID != "e1" {
		t.Fatalf("record = %+v, want the earlier e1 kept", got)
	}
}

func TestRecordUnansweredDelivery_ReplacesADeliveryFromBeforeTheRun(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")
	previousRun := time.Now().UTC().Add(-time.Hour)
	runStarted := previousRun.Add(30 * time.Minute)

	recordDeliveryForTest(t, db, "s1", "e1", previousRun, time.Time{})
	recordDeliveryForTest(t, db, "s1", "e2", runStarted.Add(time.Minute), runStarted)

	if got := unansweredDeliveryForTest(t, db, "s1"); got == nil || got.EventID != "e2" {
		t.Fatalf("record = %+v, want e2 to replace the previous run's e1", got)
	}
}

func TestWithdrawUnansweredDelivery_RemovesOnlyTheNamedEvent(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	recordDeliveryForTest(t, db, "s1", "e1", time.Now().UTC(), time.Time{})

	if err := db.WithdrawUnansweredDelivery(ctx, "s1", "e2"); err != nil {
		t.Fatalf("withdraw e2: %v", err)
	}
	if got := unansweredDeliveryForTest(t, db, "s1"); got == nil || got.EventID != "e1" {
		t.Fatalf("after withdrawing another event = %+v, want e1 kept", got)
	}
	if err := db.WithdrawUnansweredDelivery(ctx, "s1", "e1"); err != nil {
		t.Fatalf("withdraw e1: %v", err)
	}
	if got := unansweredDeliveryForTest(t, db, "s1"); got != nil {
		t.Fatalf("after withdrawing e1 = %+v, want none", got)
	}
}

func TestClearUnansweredDelivery_RemovesTheRecord(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")
	recordDeliveryForTest(t, db, "s1", "e1", time.Now().UTC(), time.Time{})

	if err := db.ClearUnansweredDelivery(context.Background(), "s1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := unansweredDeliveryForTest(t, db, "s1"); got != nil {
		t.Fatalf("after clear = %+v, want none", got)
	}
}

func TestUnansweredDelivery_BelongsToOneIncarnation(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")
	recordDeliveryForTest(t, db, "s1", "e1", time.Now().UTC(), time.Time{})

	destroyAndRecreateSessionForTest(t, db, "s1")

	if got := unansweredDeliveryForTest(t, db, "s1"); got != nil {
		t.Fatalf("recreated session inherited %+v, want none", got)
	}
}

func TestRecordUnansweredDelivery_UnknownSessionIsAnError(t *testing.T) {
	db := migratedTestDB(t)
	d := domain.UnansweredDelivery{EventID: "e1", EventType: "user.emit", DeliveredAt: time.Now().UTC()}
	if err := db.RecordUnansweredDelivery(context.Background(), "missing", d, time.Time{}); err == nil {
		t.Fatal("record for an unknown session succeeded, want an error")
	}
}
