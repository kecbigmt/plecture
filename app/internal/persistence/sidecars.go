package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Tombstone returns name's most recently destroyed incarnation. The retained
// session row and its children are the one authority for a tombstone.
func (db *DB) Tombstone(ctx context.Context, name string) (*contract.Tombstone, error) {
	var tombstone *contract.Tombstone
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		row, err := sqlcgen.New(tx).GetLatestDestroyedSession(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get destroyed session %q: %w", name, err)
		}
		session, err := sessionFromRow(row)
		if err != nil {
			return err
		}
		if err := db.loadSessionExtras(ctx, tx, session); err != nil {
			return err
		}
		tombstone = &contract.Tombstone{Session: *session, DestroyedAt: session.DestroyedAt}
		return nil
	})
	return tombstone, err
}

// SwapChainAttempt atomically replaces one incarnation-scoped fingerprint.
func (db *DB) SwapChainAttempt(ctx context.Context, session, instance, chainID, fingerprint string) (previous string, won bool, err error) {
	err = db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM chain_attempts WHERE session_id = ? AND instance = ? AND chain_id = ?`, sessionID, instance, chainID).Scan(&previous); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read chain attempt: %w", err)
			}
			previous = ""
		}
		if previous == fingerprint {
			return nil
		}
		won = true
		if fingerprint == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM chain_attempts WHERE session_id = ? AND instance = ? AND chain_id = ?`, sessionID, instance, chainID)
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chain_attempts (session_id, instance, chain_id, fingerprint) VALUES (?, ?, ?, ?)
			ON CONFLICT(session_id, instance, chain_id) DO UPDATE SET fingerprint = excluded.fingerprint`, sessionID, instance, chainID, fingerprint)
		return err
	})
	return previous, won, err
}

// RevertChainAttempt restores previous only while claimed remains current.
func (db *DB) RevertChainAttempt(ctx context.Context, session, instance, chainID, claimed, previous string) (reverted bool, err error) {
	err = db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if previous == "" {
			result, err := tx.ExecContext(ctx, `DELETE FROM chain_attempts WHERE session_id = ? AND instance = ? AND chain_id = ? AND fingerprint = ?`, sessionID, instance, chainID, claimed)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			reverted = n == 1
			return nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE chain_attempts SET fingerprint = ? WHERE session_id = ? AND instance = ? AND chain_id = ? AND fingerprint = ?`, previous, sessionID, instance, chainID, claimed)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		reverted = n == 1
		return nil
	})
	return reverted, err
}

// ClearChainAttempts removes all markers for one session incarnation.
func (db *DB) ClearChainAttempts(ctx context.Context, sessionID string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM chain_attempts WHERE session_id = ?`, sessionID)
		return err
	})
}
