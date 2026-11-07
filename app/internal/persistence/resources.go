package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/persistence/sqlcgen"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// ResourceTypeConflictError reports a resource name already recorded for
// another type. Names are unique across types because a session takes its
// name from one, so the second type is refused rather than given a row.
type ResourceTypeConflictError struct {
	Name      string
	Recorded  string
	Requested string
}

func (e *ResourceTypeConflictError) Error() string {
	recorded := fmt.Sprintf("resource %q", e.Recorded)
	if e.Recorded == "" {
		recorded = "a resource recorded without a type"
	}
	return fmt.Sprintf("resource name %q already belongs to %s, so resource %q cannot derive it", e.Name, recorded, e.Requested)
}

// ResourceByName returns the resource recorded under name, or (nil, nil).
func (db *DB) ResourceByName(ctx context.Context, name string) (*contract.Resource, error) {
	var found *contract.Resource
	err := db.WithReadTx(ctx, func(tx *sql.Tx) error {
		row, err := sqlcgen.New(tx).GetResourceByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get resource %q: %w", name, err)
		}
		found = resourceFromRow(row)
		return nil
	})
	return found, err
}

// recordResourceTx runs inside the write that references r, so no release in
// another transaction can delete the row between recording and referencing it.
// The row recorded under r's name wins over r, even over an id r carries from a
// row since released and recorded again, because the name is the identity.
func recordResourceTx(ctx context.Context, q *sqlcgen.Queries, r *contract.Resource) error {
	if r == nil {
		return nil
	}
	if r.Type == "" && r.Name == "" {
		return recordUntypedResourceTx(ctx, q, r)
	}
	if r.Name == "" {
		return fmt.Errorf("resource %q has no name", r.Locator)
	}
	row, err := q.GetResourceByName(ctx, r.Name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if r.ID == "" {
			r.ID = newULID()
		}
		if err := q.InsertResource(ctx, sqlcgen.InsertResourceParams{
			ID:        r.ID,
			Type:      r.Type,
			Name:      r.Name,
			Locator:   r.Locator,
			CreatedAt: formatTime(time.Now()),
		}); err != nil {
			return fmt.Errorf("record resource %q: %w", r.Name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get resource %q: %w", r.Name, err)
	}
	if row.Type != r.Type {
		return &ResourceTypeConflictError{Name: r.Name, Recorded: row.Type, Requested: r.Type}
	}
	*r = *resourceFromRow(row)
	return nil
}

// recordUntypedResourceTx records an identifier a one-time import carries
// without the declaration that resolved it, the same way the resources
// migration records one: a single row per identifier, named by its own id so it
// can never take a name a declaration derives.
func recordUntypedResourceTx(ctx context.Context, q *sqlcgen.Queries, r *contract.Resource) error {
	row, err := q.GetUntypedResourceByLocator(ctx, r.Locator)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id := newULID()
		*r = contract.Resource{ID: id, Name: id, Locator: r.Locator}
		if err := q.InsertResource(ctx, sqlcgen.InsertResourceParams{ID: id, Name: id, Locator: r.Locator, CreatedAt: formatTime(time.Now())}); err != nil {
			return fmt.Errorf("record resource %q: %w", r.Locator, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get resource %q: %w", r.Locator, err)
	}
	*r = *resourceFromRow(row)
	return nil
}

// releaseResourcesTx deletes each of ids that nothing references any more.
// Callers pass the ids a write could have dropped, collected before it, so a
// write never pays for a sweep of every recorded resource.
func releaseResourcesTx(ctx context.Context, q *sqlcgen.Queries, ids []string) error {
	for _, id := range ids {
		if err := q.DeleteUnreferencedResource(ctx, id); err != nil {
			return fmt.Errorf("release resource %s: %w", id, err)
		}
	}
	return nil
}

func sessionResourceIDsTx(ctx context.Context, q *sqlcgen.Queries, sessionID string) ([]string, error) {
	rows, err := q.ListSessionResourceIDs(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list resources referenced by session %s: %w", sessionID, err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Valid {
			ids = append(ids, row.String)
		}
	}
	return ids, nil
}

// fillResourcesTx completes references a row read left holding only an ID.
func fillResourcesTx(ctx context.Context, q *sqlcgen.Queries, refs []*contract.Resource) error {
	seen := map[string]bool{}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref != nil && !seen[ref.ID] {
			seen[ref.ID] = true
			ids = append(ids, ref.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.ListResourcesByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("load resources: %w", err)
	}
	byID := make(map[string]*contract.Resource, len(rows))
	for _, row := range rows {
		byID[row.ID] = resourceFromRow(row)
	}
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		found, ok := byID[ref.ID]
		if !ok {
			return fmt.Errorf("resource %s is referenced but not recorded", ref.ID)
		}
		*ref = *found
	}
	return nil
}

func resourceRef(id sql.NullString) *contract.Resource {
	if !id.Valid {
		return nil
	}
	return &contract.Resource{ID: id.String}
}

func resourceID(r *contract.Resource) sql.NullString {
	if r == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: r.ID, Valid: true}
}

func resourceFromRow(row sqlcgen.Resource) *contract.Resource {
	return &contract.Resource{ID: row.ID, Type: row.Type, Name: row.Name, Locator: row.Locator}
}

func sessionResourceRefs(sessions []*contract.Session) []*contract.Resource {
	var refs []*contract.Resource
	for _, s := range sessions {
		refs = append(refs, s.Resource)
		for _, states := range []map[string]*contract.TaskState{s.Nodes, s.Tasks} {
			for _, ts := range states {
				if ts != nil {
					refs = append(refs, ts.Resource, ts.Subscription)
				}
			}
		}
	}
	return refs
}
