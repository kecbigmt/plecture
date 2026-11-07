package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Population returns one population's durable state, or (nil, nil) if key
// has never been recorded. The population row and its members are read
// inside one transaction, so a concurrent UpdatePopulation can never be
// interleaved into a single logical value that never existed as such.
func (db *DB) Population(ctx context.Context, key contract.PopulationProvenance) (*domain.PopulationState, error) {
	var population *domain.PopulationState
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		row, err := sqlcgen.New(tx).GetPopulation(ctx, sqlcgen.GetPopulationParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("get population %+v: %w", key, err)
		}
		members, err := loadPopulationMembers(ctx, tx, key)
		if err != nil {
			return err
		}
		population = &domain.PopulationState{ProjectRoot: row.ProjectRoot, Workflow: row.Workflow, Name: row.Name, Members: members}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return population, nil
}

// UpdatePopulation reads or creates one population and its members, runs fn
// against it, and replaces that population's row and member rows — all in
// one write transaction scoped to key alone.
func (db *DB) UpdatePopulation(ctx context.Context, key contract.PopulationProvenance, fn func(*domain.PopulationState) error) error {
	return db.WithImmediateTx(ctx, func(tx *sql.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.GetPopulation(ctx, sqlcgen.GetPopulationParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name})
		var population *domain.PopulationState
		previous := map[string]bool{}
		switch {
		case errors.Is(err, sql.ErrNoRows):
			population = &domain.PopulationState{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name}
		case err != nil:
			return fmt.Errorf("get population %+v: %w", key, err)
		default:
			members, err := loadPopulationMembers(ctx, tx, key)
			if err != nil {
				return err
			}
			for _, member := range members {
				previous[member.Resource.ID] = true
			}
			population = &domain.PopulationState{ProjectRoot: row.ProjectRoot, Workflow: row.Workflow, Name: row.Name, Members: members}
		}
		if population.Members == nil {
			population.Members = make(map[string]*domain.PopulationMember)
		}

		if err := fn(population); err != nil {
			return err
		}

		// key, not whatever fn left in population's identity fields, is the FK
		// target below, so the two can never disagree.
		if err := q.UpsertPopulation(ctx, sqlcgen.UpsertPopulationParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name}); err != nil {
			return fmt.Errorf("upsert population %+v: %w", key, err)
		}
		// Kept members are upserted rather than deleted and reinserted, so a
		// resource every write still references is never released in between.
		kept := map[string]bool{}
		for _, member := range population.Members {
			if member == nil {
				continue
			}
			if err := writePopulationMemberTx(ctx, q, key, member); err != nil {
				return err
			}
			kept[member.Resource.ID] = true
		}
		for id := range previous {
			if kept[id] {
				continue
			}
			if err := q.DeletePopulationMember(ctx, sqlcgen.DeletePopulationMemberParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name, ResourceID: id}); err != nil {
				return fmt.Errorf("delete population member %s of %+v: %w", id, key, err)
			}
		}
		return nil
	})
}

func loadPopulationMembers(ctx context.Context, q sqlcgen.DBTX, key contract.PopulationProvenance) (map[string]*domain.PopulationMember, error) {
	queries := sqlcgen.New(q)
	rows, err := queries.ListPopulationMembers(ctx, sqlcgen.ListPopulationMembersParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name})
	if err != nil {
		return nil, fmt.Errorf("list population members for %+v: %w", key, err)
	}
	blockerRows, err := queries.ListPopulationMemberBlockersForPopulation(ctx, sqlcgen.ListPopulationMemberBlockersForPopulationParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name})
	if err != nil {
		return nil, fmt.Errorf("list population member blockers for %+v: %w", key, err)
	}
	blockersByResource := map[string][]string{}
	for _, b := range blockerRows {
		blockersByResource[b.ResourceID] = append(blockersByResource[b.ResourceID], b.Reason)
	}

	loaded := make([]*domain.PopulationMember, 0, len(rows))
	refs := make([]*contract.Resource, 0, len(rows))
	for _, row := range rows {
		member, err := populationMemberFromRow(row, blockersByResource[row.ResourceID])
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, member)
		refs = append(refs, &member.Resource)
	}
	if err := fillResourcesTx(ctx, queries, refs); err != nil {
		return nil, err
	}
	members := make(map[string]*domain.PopulationMember, len(loaded))
	for _, member := range loaded {
		members[member.Resource.Name] = member
	}
	return members, nil
}

// lastDecisionFromColumns rejoins decision_kind/decision_reason into the
// domain's single "kind:reason" (or bare "kind") LastDecision string.
func lastDecisionFromColumns(kind, reason sql.NullString) string {
	if !kind.Valid {
		return ""
	}
	if !reason.Valid {
		return kind.String
	}
	return kind.String + ":" + reason.String
}

// splitLastDecision is lastDecisionFromColumns's inverse.
func splitLastDecision(lastDecision string) (kind, reason sql.NullString) {
	if lastDecision == "" {
		return sql.NullString{}, sql.NullString{}
	}
	if i := strings.Index(lastDecision, ":"); i >= 0 {
		return sql.NullString{String: lastDecision[:i], Valid: true}, sql.NullString{String: lastDecision[i+1:], Valid: true}
	}
	return sql.NullString{String: lastDecision, Valid: true}, sql.NullString{}
}

func populationMemberFromRow(row sqlcgen.PopulationMember, blockers []string) (*domain.PopulationMember, error) {
	var item map[string]any
	if err := json.Unmarshal([]byte(row.ItemJson), &item); err != nil {
		return nil, fmt.Errorf("parse population member %q item: %w", row.ResourceID, err)
	}
	acceptedAt, err := parseTimeNull(row.AcceptedAt)
	if err != nil {
		return nil, fmt.Errorf("parse population member %q accepted_at: %w", row.ResourceID, err)
	}
	lastAppearance, err := parseTimeNull(row.LastAppearance)
	if err != nil {
		return nil, fmt.Errorf("parse population member %q last_appearance: %w", row.ResourceID, err)
	}
	lastInbound, err := parseTimeNull(row.LastInbound)
	if err != nil {
		return nil, fmt.Errorf("parse population member %q last_inbound: %w", row.ResourceID, err)
	}
	retryAt, err := parseTimeNull(row.AdmitRetryAt)
	if err != nil {
		return nil, fmt.Errorf("parse population member %q admit_retry_at: %w", row.ResourceID, err)
	}
	return &domain.PopulationMember{
		Resource:                 contract.Resource{ID: row.ResourceID},
		Item:                     item,
		SessionName:              row.SessionName.String,
		Generation:               uint64(row.Generation),
		AcceptedAt:               acceptedAt,
		LastAppearance:           lastAppearance,
		LastInbound:              lastInbound,
		Tombstoned:               row.Tombstoned,
		PendingUp:                row.PendingUp,
		ConsecutiveAdmitFailures: uint(row.ConsecutiveAdmitFailures),
		LastAdmitReason:          row.LastAdmitReason.String,
		LastAdmitError:           row.LastAdmitError.String,
		AdmitRetryAt:             retryAt,
		AdmitSuspended:           row.AdmitSuspended,
		LastDecision:             lastDecisionFromColumns(row.DecisionKind, row.DecisionReason),
		LastBlockers:             blockers,
	}, nil
}

func writePopulationMemberTx(ctx context.Context, q *sqlcgen.Queries, key contract.PopulationProvenance, member *domain.PopulationMember) error {
	if err := recordResourceTx(ctx, q, &member.Resource); err != nil {
		return fmt.Errorf("population %+v member: %w", key, err)
	}
	resource := member.Resource.ID
	itemJSON, err := json.Marshal(member.Item)
	if err != nil {
		return fmt.Errorf("marshal population member %q item: %w", resource, err)
	}
	decisionKind, decisionReason := splitLastDecision(member.LastDecision)
	if err := q.UpsertPopulationMember(ctx, sqlcgen.UpsertPopulationMemberParams{
		ProjectRoot:              key.ProjectRoot,
		Workflow:                 key.Workflow,
		Name:                     key.Name,
		ResourceID:               resource,
		SessionName:              nullString(member.SessionName),
		Generation:               int64(member.Generation),
		AcceptedAt:               formatTimeNull(member.AcceptedAt),
		LastAppearance:           formatTimeNull(member.LastAppearance),
		LastInbound:              formatTimeNull(member.LastInbound),
		Tombstoned:               member.Tombstoned,
		PendingUp:                member.PendingUp,
		ConsecutiveAdmitFailures: int64(member.ConsecutiveAdmitFailures),
		LastAdmitReason:          nullString(member.LastAdmitReason),
		LastAdmitError:           nullString(member.LastAdmitError),
		AdmitRetryAt:             formatTimeNull(member.AdmitRetryAt),
		AdmitSuspended:           member.AdmitSuspended,
		DecisionKind:             decisionKind,
		DecisionReason:           decisionReason,
		ItemJson:                 string(itemJSON),
	}); err != nil {
		return fmt.Errorf("write population member %q: %w", resource, err)
	}
	if err := q.DeletePopulationMemberBlockers(ctx, sqlcgen.DeletePopulationMemberBlockersParams{ProjectRoot: key.ProjectRoot, Workflow: key.Workflow, Name: key.Name, ResourceID: resource}); err != nil {
		return fmt.Errorf("clear population member %q blockers: %w", resource, err)
	}
	for i, reason := range member.LastBlockers {
		if err := q.InsertPopulationMemberBlocker(ctx, sqlcgen.InsertPopulationMemberBlockerParams{
			ProjectRoot: key.ProjectRoot,
			Workflow:    key.Workflow,
			Name:        key.Name,
			ResourceID:  resource,
			Position:    int64(i),
			Reason:      reason,
		}); err != nil {
			return fmt.Errorf("insert population member %q blocker %d: %w", resource, i, err)
		}
	}
	return nil
}
