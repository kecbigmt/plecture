package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/contracts/event"
)

func appendInboxEventForTest(t *testing.T, db *DB, session, id, supersedes string) int64 {
	t.Helper()
	seq, err := db.AppendEvent(context.Background(), event.Event{
		ID: id, SessionName: session, Time: time.Now().UTC(), Type: "user.emit",
		Source: "test", Direction: event.Inbound, Supersedes: supersedes,
	})
	if err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
	if err := db.AddInboxEntries(context.Background(), session, []int64{seq}, "runtime"); err != nil {
		t.Fatalf("add inbox entry %s: %v", id, err)
	}
	return seq
}

func claimedIDs(c InboxClaim) []string {
	ids := make([]string, 0, len(c.Events))
	for _, ev := range c.Events {
		ids = append(ids, ev.ID)
	}
	return ids
}

func TestClaimInboxBatch_ClaimsEveryPendingEntryInLogOrder(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	appendInboxEventForTest(t, db, "s1", "e2", "e1")

	claim, err := db.ClaimInboxBatch(ctx, "s1", "b1", 3)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.BatchID != "b1" || claim.Deliveries != 1 || claim.Exhausted || claim.Channel != "runtime" {
		t.Fatalf("claim = %+v, want batch b1 delivered once on channel runtime", claim)
	}
	if got := claimedIDs(claim); len(got) != 2 || got[0] != "e1" || got[1] != "e2" {
		t.Fatalf("claimed events = %v, want [e1 e2]", got)
	}
	if claim.Events[1].Supersedes != "e1" {
		t.Fatalf("claimed event lost its supersedes reference: %+v", claim.Events[1])
	}
}

func TestClaimInboxBatch_EmptyInboxClaimsNothing(t *testing.T) {
	db := migratedTestDB(t)
	createSessionForTest(t, db, "s1")

	claim, err := db.ClaimInboxBatch(context.Background(), "s1", "b1", 3)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.BatchID != "" || len(claim.Events) != 0 {
		t.Fatalf("claim on an empty inbox = %+v, want the zero claim", claim)
	}
}

func TestClaimInboxBatch_UnacknowledgedBatchIsReturnedAgainUnchanged(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	if _, err := db.ClaimInboxBatch(ctx, "s1", "b1", 3); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	appendInboxEventForTest(t, db, "s1", "e2", "")

	again, err := db.ClaimInboxBatch(ctx, "s1", "b2", 3)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if again.BatchID != "b1" || again.Deliveries != 2 {
		t.Fatalf("second claim = %+v, want batch b1 delivered twice", again)
	}
	if got := claimedIDs(again); len(got) != 1 || got[0] != "e1" {
		t.Fatalf("redelivered events = %v, want [e1] (an entry that arrived later waits)", got)
	}
}

func TestClaimInboxBatch_ReportsExhaustionWithoutCountingAnotherDelivery(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	for i := 0; i < 3; i++ {
		if _, err := db.ClaimInboxBatch(ctx, "s1", "b1", 3); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}

	exhausted, err := db.ClaimInboxBatch(ctx, "s1", "b2", 3)
	if err != nil {
		t.Fatalf("fourth claim: %v", err)
	}
	if !exhausted.Exhausted || exhausted.BatchID != "b1" || exhausted.Deliveries != 3 {
		t.Fatalf("fourth claim = %+v, want batch b1 exhausted at 3 deliveries", exhausted)
	}
}

func TestRemoveInboxBatch_RemovesOnlyTheNamedBatch(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	if _, err := db.ClaimInboxBatch(ctx, "s1", "b1", 3); err != nil {
		t.Fatalf("claim: %v", err)
	}
	appendInboxEventForTest(t, db, "s1", "e2", "")

	if removed, err := db.RemoveInboxBatch(ctx, "s1", "other"); err != nil || removed != 0 {
		t.Fatalf("remove unknown batch = (%d, %v), want (0, nil)", removed, err)
	}
	removed, err := db.RemoveInboxBatch(ctx, "s1", "b1")
	if err != nil || removed != 1 {
		t.Fatalf("remove b1 = (%d, %v), want (1, nil)", removed, err)
	}
	next, err := db.ClaimInboxBatch(ctx, "s1", "b2", 3)
	if err != nil {
		t.Fatalf("claim after remove: %v", err)
	}
	if got := claimedIDs(next); next.BatchID != "b2" || len(got) != 1 || got[0] != "e2" {
		t.Fatalf("claim after remove = %+v, want batch b2 with [e2]", next)
	}
}

func TestAddInboxEntries_IsIdempotentPerEvent(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	seq := appendInboxEventForTest(t, db, "s1", "e1", "")
	if err := db.AddInboxEntries(ctx, "s1", []int64{seq}, "runtime"); err != nil {
		t.Fatalf("second add: %v", err)
	}

	pending, err := db.InboxPending(ctx, "s1")
	if err != nil || pending != 1 {
		t.Fatalf("pending = (%d, %v), want (1, nil)", pending, err)
	}
}

func TestInbox_UnknownSessionIsAnError(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	if err := db.AddInboxEntries(ctx, "missing", []int64{1}, "runtime"); err == nil {
		t.Fatal("AddInboxEntries on an unknown session succeeded")
	}
	if _, err := db.ClaimInboxBatch(ctx, "missing", "b1", 3); err == nil {
		t.Fatal("ClaimInboxBatch on an unknown session succeeded")
	}
	if _, err := db.RemoveInboxBatch(ctx, "missing", "b1"); err == nil {
		t.Fatal("RemoveInboxBatch on an unknown session succeeded")
	}
}

func TestInbox_RecreatedSessionStartsEmpty(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	destroyAndRecreateSessionForTest(t, db, "s1")

	pending, err := db.InboxPending(ctx, "s1")
	if err != nil || pending != 0 {
		t.Fatalf("pending for the new incarnation = (%d, %v), want (0, nil)", pending, err)
	}
}

func TestClearInboxEntries_RemovesOneIncarnationsEntries(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	createSessionForTest(t, db, "s1")
	createSessionForTest(t, db, "s2")
	appendInboxEventForTest(t, db, "s1", "e1", "")
	appendInboxEventForTest(t, db, "s2", "e2", "")
	id, err := db.EventStreamID(ctx, "s1")
	if err != nil {
		t.Fatalf("stream id: %v", err)
	}

	if err := db.ClearInboxEntries(ctx, id); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if pending, err := db.InboxPending(ctx, "s1"); err != nil || pending != 0 {
		t.Fatalf("s1 pending = (%d, %v), want (0, nil)", pending, err)
	}
	if pending, err := db.InboxPending(ctx, "s2"); err != nil || pending != 1 {
		t.Fatalf("s2 pending = (%d, %v), want (1, nil)", pending, err)
	}
}
