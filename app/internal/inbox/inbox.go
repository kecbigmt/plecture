// Package inbox holds the events a session's runtime has not acknowledged and
// applies the one delivery rule every consumer shares: a pending event that
// another pending event supersedes is not delivered, and what is pending is
// delivered together.
package inbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/datahome"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/persistence"
	"github.com/kecbigmt/plecture/contracts/event"
)

// MaxDeliveries bounds how often one batch is handed out. Deliveries are
// counted rather than reported failures, because a consumer that crashes on a
// batch reports nothing and would otherwise receive it forever.
const MaxDeliveries = 3

var errUnacknowledged = errors.New("the batch was not acknowledged")

// Superseded returns the ids in evs that another event in evs names in
// supersedes. Only ids and the reference are read, so the rule cannot depend
// on an event's type, source, or text.
func Superseded(evs []event.Event) map[string]bool {
	present := make(map[string]bool, len(evs))
	for _, ev := range evs {
		present[ev.ID] = true
	}
	var omitted map[string]bool
	for _, ev := range evs {
		if ev.Supersedes == "" || ev.Supersedes == ev.ID || !present[ev.Supersedes] {
			continue
		}
		if omitted == nil {
			omitted = map[string]bool{}
		}
		omitted[ev.Supersedes] = true
	}
	return omitted
}

// Batch is what one take delivers.
type Batch struct {
	ID         string        `json:"batch"`
	Deliveries int           `json:"deliveries"`
	Events     []event.Event `json:"events"`
}

type Store struct {
	dir string
}

// NewStore defaults an empty dir the way eventlog.NewStore does, so both
// stores of one process read the same database.
func NewStore(dir string) *Store {
	if dir == "" {
		dir = datahome.Resolve()
	}
	return &Store{dir: dir}
}

func (s *Store) db() (*persistence.DB, error) {
	db, err := persistence.EnsureCurrentShared(context.Background(), persistence.PathIn(s.dir))
	if err != nil {
		return nil, fmt.Errorf("inbox: open database: %w", err)
	}
	return db, nil
}

// Add hands the events at sequences to the session's inbox on behalf of the
// channel binding that selected them, all or none.
func (s *Store) Add(session string, sequences []int64, channelName string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	return db.AddInboxEntries(context.Background(), session, sequences, channelName)
}

func (s *Store) Pending(session string) (bool, error) {
	db, err := s.db()
	if err != nil {
		return false, err
	}
	count, err := db.InboxPending(context.Background(), session)
	return count > 0, err
}

// Take returns nil when nothing is pending.
func (s *Store) Take(session string) (*Batch, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	for {
		id, err := newBatchID()
		if err != nil {
			return nil, err
		}
		claim, err := db.ClaimInboxBatch(context.Background(), session, id, MaxDeliveries)
		if err != nil {
			return nil, err
		}
		if claim.BatchID == "" {
			return nil, nil
		}
		omitted := Superseded(claim.Events)
		retained := make([]event.Event, 0, len(claim.Events))
		for _, ev := range claim.Events {
			if !omitted[ev.ID] {
				retained = append(retained, ev)
			}
		}
		if !claim.Exhausted {
			return &Batch{ID: claim.BatchID, Deliveries: claim.Deliveries, Events: retained}, nil
		}
		if err := s.park(db, session, claim, retained); err != nil {
			return nil, err
		}
	}
}

// park reports before it removes: a crash in between repeats the report on the
// next take instead of dropping the batch without a trace.
func (s *Store) park(db *persistence.DB, session string, claim persistence.InboxClaim, retained []event.Event) error {
	log := eventlog.NewStore(s.dir)
	for _, ev := range retained {
		if _, _, _, err := log.Append(channel.ChannelErrorEvent(ev, claim.Channel, claim.Deliveries, errUnacknowledged)); err != nil {
			return fmt.Errorf("inbox: record parked event %s: %w", ev.ID, err)
		}
	}
	if _, err := db.RemoveInboxBatch(context.Background(), session, claim.BatchID); err != nil {
		return err
	}
	return nil
}

func (s *Store) Ack(session, batchID string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	removed, err := db.RemoveInboxBatch(context.Background(), session, batchID)
	if err != nil {
		return err
	}
	if removed == 0 {
		return fmt.Errorf("inbox: session %s has no outstanding batch %q", session, batchID)
	}
	return nil
}

// Clear drops every entry of one session incarnation, named by its id.
func (s *Store) Clear(sessionID string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	return db.ClearInboxEntries(context.Background(), sessionID)
}

func newBatchID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("inbox: mint batch id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
