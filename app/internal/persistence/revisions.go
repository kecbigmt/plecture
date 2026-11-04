package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func writeWorkflowRevisionTx(ctx context.Context, tx *sql.Tx, s *domain.Session) error {
	if s.WorkflowRevision == "" {
		if s.WorkflowRevisionBody != "" {
			return fmt.Errorf("session %q has revision body without digest", s.Name)
		}
		return nil
	}
	if !json.Valid([]byte(s.WorkflowRevisionBody)) {
		return fmt.Errorf("session %q has invalid workflow revision body", s.Name)
	}
	sum := sha256.Sum256([]byte(s.WorkflowRevisionBody))
	if s.WorkflowRevision != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("session %q workflow revision digest mismatch", s.Name)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO workflow_revisions(digest, workflow, body, created_at)
		VALUES(?, ?, ?, ?) ON CONFLICT(digest) DO NOTHING`, s.WorkflowRevision, s.Workflow, s.WorkflowRevisionBody, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("save workflow revision for %q: %w", s.Name, err)
	}
	return nil
}

func loadWorkflowRevisionBody(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, s *domain.Session) error {
	if s.WorkflowRevision == "" {
		return nil
	}
	if err := q.QueryRowContext(ctx, `SELECT body FROM workflow_revisions WHERE digest = ?`, s.WorkflowRevision).Scan(&s.WorkflowRevisionBody); err != nil {
		return fmt.Errorf("load workflow revision for session %q: %w", s.Name, err)
	}
	return nil
}

func (db *DB) PruneWorkflowRevisions(ctx context.Context) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM workflow_revisions WHERE digest NOT IN
			(SELECT workflow_revision FROM sessions WHERE status <> 'destroyed' AND workflow_revision IS NOT NULL)`)
		return err
	})
}

func (db *DB) NeedsWorkflowRevisionBackfill(ctx context.Context) (bool, error) {
	var pending bool
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions
			WHERE status <> 'destroyed' AND workflow <> '' AND workflow_revision IS NULL)`).Scan(&pending)
	})
	return pending, err
}
