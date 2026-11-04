package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestWorkflowRevisionsShareBodyAndPruneOnlyUnreferenced(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	body := `{"workflow":{"ID":"work"}}`
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])
	for _, name := range []string{"first", "second"} {
		now := time.Now()
		if err := db.PutSession(ctx, &domain.Session{
			Name: name, Workflow: "work", WorkflowRevision: digest, WorkflowRevisionBody: body,
			Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.write.QueryRowContext(ctx, "SELECT count(*) FROM workflow_revisions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("revision rows = %d, want one shared body", count)
	}
	got, err := db.GetSession(ctx, "second")
	if err != nil || got.WorkflowRevisionBody != body || got.WorkflowRevision != digest {
		t.Fatalf("loaded revision = %+v, err %v", got, err)
	}
	if err := db.DestroySession(ctx, "first", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneWorkflowRevisions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.write.QueryRowContext(ctx, "SELECT count(*) FROM workflow_revisions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("referenced revision count = %d, err %v", count, err)
	}
	if err := db.DestroySession(ctx, "second", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneWorkflowRevisions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.write.QueryRowContext(ctx, "SELECT count(*) FROM workflow_revisions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unreferenced revision count = %d, err %v", count, err)
	}
}

func TestWorkflowRevisionBackfillRunsOnlyOnce(t *testing.T) {
	db := migratedTestDB(t)
	ctx := context.Background()
	pending, err := db.NeedsWorkflowRevisionBackfill(ctx)
	if err != nil || pending {
		t.Fatalf("initial backfill pending = %t, %v", pending, err)
	}
	if err := db.CompleteWorkflowRevisionBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.PutSession(ctx, &domain.Session{Name: "failed-setup", Workflow: "removed", Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	pending, err = db.NeedsWorkflowRevisionBackfill(ctx)
	if err != nil || pending {
		t.Fatalf("later incomplete session triggered backfill = %t, %v", pending, err)
	}
}
