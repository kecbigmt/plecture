package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
)

// RecordUnansweredDelivery keeps every unanswered delivery rather than the
// earliest alone: a status that answers the earliest must not take a later
// one with it. Deliveries before staleBefore are dropped, because a previous
// run's record accuses no current runtime.
func (db *DB) RecordUnansweredDelivery(ctx context.Context, session string, d domain.UnansweredDelivery, staleBefore time.Time) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := q.DeleteUnansweredDeliveriesBefore(ctx, sqlcgen.DeleteUnansweredDeliveriesBeforeParams{SessionID: sessionID, DeliveredAt: formatTime(staleBefore)}); err != nil {
			return fmt.Errorf("drop stale deliveries for %q: %w", session, err)
		}
		if err := q.InsertUnansweredDelivery(ctx, sqlcgen.InsertUnansweredDeliveryParams{
			SessionID:   sessionID,
			EventID:     d.EventID,
			EventType:   d.EventType,
			DeliveredAt: formatTime(d.DeliveredAt),
		}); err != nil {
			return fmt.Errorf("record unanswered delivery for %q: %w", session, err)
		}
		return nil
	})
}

// UnansweredDelivery returns the earliest unanswered delivery, or nil when
// every delivery has been answered.
func (db *DB) UnansweredDelivery(ctx context.Context, session string) (*domain.UnansweredDelivery, error) {
	var out *domain.UnansweredDelivery
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		out, err = getUnansweredDelivery(ctx, q, sessionID)
		return err
	})
	return out, err
}

func (db *DB) WithdrawUnansweredDelivery(ctx context.Context, session, eventID string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := q.DeleteUnansweredDeliveryForEvent(ctx, sqlcgen.DeleteUnansweredDeliveryForEventParams{SessionID: sessionID, EventID: eventID}); err != nil {
			return fmt.Errorf("withdraw unanswered delivery %q for %q: %w", eventID, session, err)
		}
		return nil
	})
}

// AnswerUnansweredDelivery compares delivery and status times instead of
// deleting unconditionally: a delivery recorded after the status was written
// and before this call runs is one the status could not have answered.
func (db *DB) AnswerUnansweredDelivery(ctx context.Context, session string, statusAt time.Time) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := q.DeleteUnansweredDeliveryThrough(ctx, sqlcgen.DeleteUnansweredDeliveryThroughParams{SessionID: sessionID, DeliveredAt: formatTime(statusAt)}); err != nil {
			return fmt.Errorf("answer unanswered delivery for %q: %w", session, err)
		}
		return nil
	})
}

func getUnansweredDelivery(ctx context.Context, q *sqlcgen.Queries, sessionID string) (*domain.UnansweredDelivery, error) {
	row, err := q.GetEarliestUnansweredDelivery(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get unanswered delivery for session id %q: %w", sessionID, err)
	}
	at, err := parseTime(row.DeliveredAt)
	if err != nil {
		return nil, fmt.Errorf("parse unanswered delivery time for session id %q: %w", sessionID, err)
	}
	return &domain.UnansweredDelivery{EventID: row.EventID, EventType: row.EventType, DeliveredAt: at}, nil
}
