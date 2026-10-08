package persistence

import (
	"context"
	"database/sql"
	"fmt"
)

// ShadowedExecution is an unreleased node execution that a newer, cleaned
// execution of the same node outranks, so a session load reads the cleaned
// one as current while the unreleased one still blocks a fresh setup.
type ShadowedExecution struct {
	SessionID   string
	SessionName string
	NodeID      string
	ExecutionID string
	Status      string
	Sequence    int64
	// NewSequence is set only on the result of a repair.
	NewSequence int64
}

type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// An unreleased row is unique per node (node_executions_one_unreleased_idx),
// so every execution that outranks one is a released generation.
const shadowedExecutionsQuery = `
SELECT u.session_id, s.name, u.node_id, u.id, u.status, u.sequence
FROM node_executions u
JOIN sessions s ON s.id = u.session_id
WHERE u.status <> 'cleaned'
AND EXISTS (
    SELECT 1 FROM node_executions newer
    WHERE newer.session_id = u.session_id AND newer.node_id = u.node_id
      AND (newer.sequence > u.sequence OR (newer.sequence = u.sequence AND newer.id > u.id))
)
ORDER BY u.session_id, u.node_id`

func listShadowedExecutions(ctx context.Context, q rowQuerier) ([]ShadowedExecution, error) {
	rows, err := q.QueryContext(ctx, shadowedExecutionsQuery)
	if err != nil {
		return nil, fmt.Errorf("list shadowed node executions: %w", err)
	}
	defer rows.Close()
	var found []ShadowedExecution
	for rows.Next() {
		var e ShadowedExecution
		if err := rows.Scan(&e.SessionID, &e.SessionName, &e.NodeID, &e.ExecutionID, &e.Status, &e.Sequence); err != nil {
			return nil, fmt.Errorf("scan shadowed node execution: %w", err)
		}
		found = append(found, e)
	}
	return found, rows.Err()
}

// ShadowedNodeExecutions reads the database at path through a read-only
// connection, so a preview neither migrates it nor creates gate files.
func ShadowedNodeExecutions(ctx context.Context, path string) ([]ShadowedExecution, error) {
	if _, err := journalMode(); err != nil {
		return nil, err
	}
	raw, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=%d", path, busyTimeoutMillis))
	if err != nil {
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	defer raw.Close()
	return listShadowedExecutions(ctx, raw)
}

// RepairShadowedNodeExecutions moves each shadowed execution's sequence above
// every sequence its session records (node executions and task instances
// share one sequence space), which makes it the node's current execution.
// Only the sequence column changes: no row, output or history is deleted.
func (db *DB) RepairShadowedNodeExecutions(ctx context.Context) ([]ShadowedExecution, error) {
	var repaired []ShadowedExecution
	err := db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		shadowed, err := listShadowedExecutions(ctx, tx)
		if err != nil {
			return err
		}
		for _, e := range shadowed {
			var highest int64
			if err := tx.QueryRowContext(ctx, `
SELECT MAX(
    COALESCE((SELECT MAX(sequence) FROM node_executions WHERE session_id = ?1), 0),
    COALESCE((SELECT MAX(sequence) FROM task_instances WHERE session_id = ?1), 0))`, e.SessionID).Scan(&highest); err != nil {
				return fmt.Errorf("read highest sequence for session %q: %w", e.SessionName, err)
			}
			e.NewSequence = highest + 1
			if _, err := tx.ExecContext(ctx, `UPDATE node_executions SET sequence = ? WHERE id = ?`, e.NewSequence, e.ExecutionID); err != nil {
				return fmt.Errorf("resequence node execution %q: %w", e.ExecutionID, err)
			}
			repaired = append(repaired, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repaired, nil
}
