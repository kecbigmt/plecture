package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
	"github.com/kecbigmt/plecture/contracts/event"
)

// InboxClaim is one batch of a session's inbox. The zero value means nothing
// is pending.
type InboxClaim struct {
	BatchID    string
	Channel    string
	Deliveries int
	// Exhausted leaves the batch in place: the caller reports it before
	// removing it, so a crash in between repeats the report, never loses it.
	Exhausted bool
	Events    []event.Event
}

// AddInboxEntries adds every sequence in one transaction, so a take sees the
// events of one hand-over together or not at all. It is idempotent per event:
// a dispatcher that replays an event after a crash does not hand it over twice.
func (db *DB) AddInboxEntries(ctx context.Context, session string, sequences []int64, channel string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		for _, sequence := range sequences {
			if err := q.InsertInboxEntry(ctx, sqlcgen.InsertInboxEntryParams{SessionID: sessionID, Sequence: sequence, Channel: channel}); err != nil {
				return fmt.Errorf("add inbox entry %d for %q: %w", sequence, session, err)
			}
		}
		return nil
	})
}

// ClaimInboxBatch returns the unacknowledged batch again when there is one,
// and claims every pending entry under newBatchID otherwise. Entries added
// while a batch is unacknowledged stay out of it: a redelivered batch must be
// the batch that was delivered before.
func (db *DB) ClaimInboxBatch(ctx context.Context, session, newBatchID string, maxDeliveries int) (InboxClaim, error) {
	var claim InboxClaim
	err := db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		rows, err := q.ListInboxEntries(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("list inbox entries for %q: %w", session, err)
		}
		var outstanding, pending []sqlcgen.ListInboxEntriesRow
		for _, row := range rows {
			if row.BatchID.Valid {
				outstanding = append(outstanding, row)
			} else {
				pending = append(pending, row)
			}
		}
		chosen := pending
		switch {
		case len(outstanding) > 0:
			chosen = outstanding
			claim.BatchID = outstanding[0].BatchID.String
			claim.Deliveries = int(outstanding[0].Deliveries)
			if claim.Deliveries >= maxDeliveries {
				claim.Exhausted = true
				break
			}
			if err := q.CountInboxBatchDelivery(ctx, sqlcgen.CountInboxBatchDeliveryParams{SessionID: sessionID, BatchID: outstanding[0].BatchID}); err != nil {
				return fmt.Errorf("count delivery of inbox batch %q: %w", claim.BatchID, err)
			}
			claim.Deliveries++
		case len(pending) > 0:
			if err := q.ClaimInboxEntries(ctx, sqlcgen.ClaimInboxEntriesParams{BatchID: sql.NullString{String: newBatchID, Valid: true}, SessionID: sessionID}); err != nil {
				return fmt.Errorf("claim inbox entries for %q: %w", session, err)
			}
			claim.BatchID = newBatchID
			claim.Deliveries = 1
		default:
			return nil
		}
		claim.Channel = chosen[0].Channel
		for _, row := range chosen {
			ev, err := eventFromRow(sqlcgen.ListEventsFromBySessionRow{
				ID: row.ID, Sequence: row.Sequence, Time: row.Time, Type: row.Type, Source: row.Source,
				Direction: row.Direction, Summary: row.Summary, Body: row.Body, Supersedes: row.Supersedes, MetadataJson: row.MetadataJson,
			}, session)
			if err != nil {
				return err
			}
			claim.Events = append(claim.Events, ev)
		}
		return nil
	})
	if err != nil {
		return InboxClaim{}, err
	}
	return claim, nil
}

// RemoveInboxBatch reports how many entries it removed; zero means batchID
// names no outstanding batch.
func (db *DB) RemoveInboxBatch(ctx context.Context, session, batchID string) (int64, error) {
	var removed int64
	err := db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		removed, err = q.DeleteInboxBatch(ctx, sqlcgen.DeleteInboxBatchParams{SessionID: sessionID, BatchID: sql.NullString{String: batchID, Valid: true}})
		if err != nil {
			return fmt.Errorf("remove inbox batch %q for %q: %w", batchID, session, err)
		}
		return nil
	})
	return removed, err
}

func (db *DB) InboxPending(ctx context.Context, session string) (int64, error) {
	var count int64
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		count, err = q.CountInboxEntries(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("count inbox entries for %q: %w", session, err)
		}
		return nil
	})
	return count, err
}

// ClearInboxEntries takes the incarnation's own id: by the time a destroy
// calls it the session's name no longer resolves to that row, and the
// tombstone it leaves behind never cascades.
func (db *DB) ClearInboxEntries(ctx context.Context, sessionID string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM inbox_entries WHERE session_id = ?`, sessionID)
		return err
	})
}
