package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type Subscription struct {
	SessionID  string            `json:"session_id"`
	Session    string            `json:"session"`
	Resource   contract.Resource `json:"resource"`
	Source     string            `json:"source"`
	InputsJSON string            `json:"inputs_json"`
	CreatedAt  string            `json:"created_at"`
}

func (db *DB) AddSubscription(ctx context.Context, session string, resource *contract.Resource, source string, inputs map[string]any, maxSubscribers int) error {
	if source != "runtime" && source != "dispatch" && source != "task" && source != "node" {
		return fmt.Errorf("invalid subscription source %q", source)
	}
	if resource == nil || resource.Locator == "" {
		return fmt.Errorf("subscription resource is required")
	}
	inputsJSON, err := marshalJSONObject(inputs)
	if err != nil {
		return fmt.Errorf("subscription inputs: %w", err)
	}
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		id, err := currentSessionID(ctx, q, session)
		if err != nil {
			return err
		}
		if err := recordResourceTx(ctx, q, resource); err != nil {
			return err
		}
		limit := sql.NullInt64{Int64: int64(maxSubscribers), Valid: maxSubscribers > 0}
		if _, err := tx.ExecContext(ctx, `UPDATE resources SET max_subscribers = ? WHERE id = ?`, limit, resource.ID); err != nil {
			return err
		}
		if err := checkSubscriberLimitTx(ctx, tx, id, resource.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(session_id, resource_id, source) DO NOTHING`, id, resource.ID, source, inputsJSON, formatTime(time.Now()))
		return err
	})
}

func (db *DB) RemoveSubscription(ctx context.Context, session, locator, source string) error {
	return db.RemoveSubscriptionByIdentity(ctx, session, locator, "", source)
}

func (db *DB) RemoveSubscriptionByIdentity(ctx context.Context, session, locator, name, source string) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		id, err := currentSessionID(ctx, sqlcgen.New(tx), session)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE session_id = ? AND source = ? AND resource_id IN (SELECT id FROM resources WHERE locator = ? OR (? <> '' AND name = ?))`, id, source, locator, name, name)
		return err
	})
}

func (db *DB) ListSubscriptions(ctx context.Context, session, resourceType string) ([]Subscription, error) {
	var result []Subscription
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT sub.session_id, s.name, r.id, r.type, r.name, r.locator, sub.source, sub.inputs_json, sub.created_at FROM subscriptions sub JOIN sessions s ON s.id = sub.session_id JOIN resources r ON r.id = sub.resource_id WHERE s.status <> 'destroyed' AND (? = '' OR s.name = ?) AND (? = '' OR r.type = ?) ORDER BY s.name, r.locator, sub.source`, session, session, resourceType, resourceType)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row Subscription
			if err := rows.Scan(&row.SessionID, &row.Session, &row.Resource.ID, &row.Resource.Type, &row.Resource.Name, &row.Resource.Locator, &row.Source, &row.InputsJSON, &row.CreatedAt); err != nil {
				return err
			}
			result = append(result, row)
		}
		return rows.Err()
	})
	return result, err
}

func (db *DB) SubscriptionSessions(ctx context.Context, locator, name string) ([]string, error) {
	var names []string
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT s.name FROM subscriptions sub JOIN sessions s ON s.id = sub.session_id JOIN resources r ON r.id = sub.resource_id WHERE s.status <> 'destroyed' AND (r.locator = ? OR (? <> '' AND r.name = ?)) ORDER BY s.name`, locator, name, name)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
		}
		return rows.Err()
	})
	return names, err
}

func reconcileSubscriptionsTx(ctx context.Context, tx *sql.Tx, sessionID string, destroyed bool, entryResourceID string) error {
	if destroyed {
		_, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE session_id = ?`, sessionID)
		return err
	}
	if entryResourceID != "" {
		if err := checkSubscriberLimitTx(ctx, tx, sessionID, entryResourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at) VALUES (?, ?, 'dispatch', '{}', ?) ON CONFLICT DO NOTHING`, sessionID, entryResourceID, formatTime(time.Now())); err != nil {
			return err
		}
	}
	for _, kind := range []struct {
		source string
		query  string
	}{
		{"task", `SELECT DISTINCT resource_id FROM task_instances WHERE session_id = ? AND status <> 'cleaned' AND resource_id IS NOT NULL`},
		{"node", `SELECT DISTINCT subscription_id FROM node_executions WHERE session_id = ? AND status <> 'cleaned' AND subscription_id IS NOT NULL`},
	} {
		rows, err := tx.QueryContext(ctx, kind.query, sessionID)
		if err != nil {
			return err
		}
		var wanted []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			wanted = append(wanted, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, resourceID := range wanted {
			if err := checkSubscriberLimitTx(ctx, tx, sessionID, resourceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at) VALUES (?, ?, ?, '{}', ?) ON CONFLICT DO NOTHING`, sessionID, resourceID, kind.source, formatTime(time.Now())); err != nil {
				return err
			}
		}
		column := "resource_id"
		if kind.source == "node" {
			column = "subscription_id"
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE session_id = ? AND source = ? AND resource_id NOT IN (SELECT `+column+` FROM `+map[string]string{"task": "task_instances", "node": "node_executions"}[kind.source]+` WHERE session_id = ? AND status <> 'cleaned' AND `+column+` IS NOT NULL)`, sessionID, kind.source, sessionID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE session_id = ? AND source = 'dispatch' AND resource_id <> ?`, sessionID, entryResourceID)
	return err
}

func checkSubscriberLimitTx(ctx context.Context, tx *sql.Tx, sessionID, resourceID string) error {
	var limit sql.NullInt64
	var locator string
	if err := tx.QueryRowContext(ctx, `SELECT max_subscribers, locator FROM resources WHERE id = ?`, resourceID).Scan(&limit, &locator); err != nil {
		return err
	}
	if !limit.Valid {
		return nil
	}
	var already bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE resource_id = ? AND session_id = ?)`, resourceID, sessionID).Scan(&already); err != nil {
		return err
	}
	if already {
		return nil
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT session_id) FROM subscriptions WHERE resource_id = ?`, resourceID).Scan(&count); err != nil {
		return err
	}
	if count >= limit.Int64 {
		return fmt.Errorf("resource %q has reached max_subscribers (%d)", locator, limit.Int64)
	}
	return nil
}
