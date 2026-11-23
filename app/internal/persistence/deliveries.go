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

// RecordUnansweredDelivery keeps an existing record unless it predates
// staleBefore: a later delivery must not move the clock of one still
// waiting for its turn, but a previous run's record accuses no current
// runtime.
func (db *DB) RecordUnansweredDelivery(ctx context.Context, session string, d domain.UnansweredDelivery, staleBefore time.Time) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		existing, err := getUnansweredDelivery(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if existing != nil && !existing.DeliveredAt.Before(staleBefore) {
			return nil
		}
		if err := q.PutUnansweredDelivery(ctx, sqlcgen.PutUnansweredDeliveryParams{
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

// UnansweredDelivery returns nil when every delivery has been answered.
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

// WithdrawUnansweredDelivery removes the record only while it still names
// eventID, so a failed attempt never erases another event's delivery.
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

func (db *DB) ClearUnansweredDelivery(ctx context.Context, session string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		sessionID, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := q.DeleteUnansweredDelivery(ctx, sessionID); err != nil {
			return fmt.Errorf("clear unanswered delivery for %q: %w", session, err)
		}
		return nil
	})
}

func getUnansweredDelivery(ctx context.Context, q *sqlcgen.Queries, sessionID string) (*domain.UnansweredDelivery, error) {
	row, err := q.GetUnansweredDelivery(ctx, sessionID)
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
