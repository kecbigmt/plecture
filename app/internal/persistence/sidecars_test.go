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
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", Resource: testResource("old", "resource://old"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	oldID, err := db.EventStreamID(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DestroySession(ctx, "same-name", now); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSession(ctx, &domain.Session{Name: "same-name", Resource: testResource("new", "resource://new"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	tombstone, err := db.Tombstone(ctx, "same-name")
	if err != nil {
		t.Fatal(err)
	}
	if tombstone == nil || tombstone.ID != oldID || tombstone.Resource == nil || tombstone.Resource.Locator != "resource://old" || tombstone.DestroyedAt.IsZero() {
		t.Fatalf("tombstone = %+v, want retained destroyed incarnation", tombstone)
	}
}
