package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func TestTombstoneReadsRetainedDestroyedIncarnation(t *testing.T) {
	ctx := context.Background()
	db := migratedTestDB(t)
	now := time.Now().UTC()
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", ResourceID: "resource://old", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	oldID, err := db.EventStreamID(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DestroySession(ctx, "same-name", now); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", ResourceID: "resource://new", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	tombstone, err := db.Tombstone(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if tombstone == nil || tombstone.ID != oldID || tombstone.ResourceID != "resource://old" || tombstone.DestroyedAt.IsZero() {
		t.Fatalf("tombstone = %+v, want retained destroyed incarnation", tombstone)
	}
}

func TestSubscriptionRetryForDestroyedIncarnationDoesNotAttachToNameReuse(t *testing.T) {
	ctx := context.Background()
	db := migratedTestDB(t)
	now := time.Now().UTC()
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	oldID, err := db.EventStreamID(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DestroySession(ctx, "same-name", now); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueSubscriptionRetryByID(ctx, oldID, subscriptionRetryUnsubscribe, "resource://old", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	live, err := db.SubscriptionRetriesForLiveSession(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("live retries = %+v, want none for replacement incarnation", live)
	}
	destroyed, err := db.DestroyedSubscriptionRetries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(destroyed) != 1 || destroyed[0].SessionID != oldID {
		t.Fatalf("destroyed retries = %+v, want old incarnation", destroyed)
	}
}

func TestSubscriptionRetryCarriesTheLatestQueuedBindingInputs(t *testing.T) {
	ctx := context.Background()
	db := migratedTestDB(t)
	now := time.Now().UTC()
	if err := db.PutSession(ctx, &domain.Session{Name: "s1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetrySubscribe, "resource://one", map[string]any{"token": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetrySubscribe, "resource://one", map[string]any{"token": "second"}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueSubscriptionRetry(ctx, "s1", subscriptionRetryUnsubscribe, "resource://two", nil); err != nil {
		t.Fatal(err)
	}
	retries, err := db.SubscriptionRetriesForLiveSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(retries) != 2 {
		t.Fatalf("retries = %+v, want one per (action, resource)", retries)
	}
	byResource := map[string]SubscriptionRetry{}
	for _, r := range retries {
		byResource[r.ResourceID] = r
	}
	if got := byResource["resource://one"].Inputs["token"]; got != "second" {
		t.Errorf("resource://one inputs token = %v, want the last queued object", got)
	}
	if got := byResource["resource://two"].Inputs; len(got) != 0 {
		t.Errorf("resource://two inputs = %v, want empty", got)
	}
}
