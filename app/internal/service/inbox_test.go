package service

import (
	"errors"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/inbox"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
)

func inboxSessionWithPendingEvent(t *testing.T, name string) (*state.Store, *inbox.Store) {
	t.Helper()
	store := state.NewStore(t.TempDir())
	if err := store.Put(&domain.Session{Name: name}); err != nil {
		t.Fatal(err)
	}
	_, seq, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{SessionName: name, Type: event.TypeUserEmit, Direction: event.Inbound, Body: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	box := inbox.NewStore(store.Dir())
	if err := box.Add(name, []int64{seq}, "runtime"); err != nil {
		t.Fatal(err)
	}
	return store, box
}

func wantServiceError(t *testing.T, err error, code string) {
	t.Helper()
	var svcErr *Error
	if !errors.As(err, &svcErr) || svcErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestInboxTake_ReturnsTheBatchOfASessionInsideTheGuard(t *testing.T) {
	store, _ := inboxSessionWithPendingEvent(t, "acme/inbox")

	batch, err := InboxTake(&config.Config{SessionGuard: "^acme/"}, store, "acme/inbox", 0)

	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if batch == nil || batch.Deliveries != 1 || len(batch.Events) != 1 {
		t.Fatalf("batch = %+v, want one delivery of one event", batch)
	}
}

func TestInboxTake_OutsideTheGuardIsRejectedAndCountsNoDelivery(t *testing.T) {
	store, box := inboxSessionWithPendingEvent(t, "other/inbox")

	_, err := InboxTake(&config.Config{SessionGuard: "^acme/"}, store, "other/inbox", 0)

	wantServiceError(t, err, ErrRepoNotAllowed)
	batch, err := box.Take("other/inbox")
	if err != nil || batch == nil || batch.Deliveries != 1 {
		t.Fatalf("the session's own take = %+v (err %v), want a first delivery", batch, err)
	}
}

func TestInboxTake_UnknownSessionErrors(t *testing.T) {
	store := state.NewStore(t.TempDir())

	_, err := InboxTake(nil, store, "owner/never-created", 0)

	wantServiceError(t, err, ErrSessionNotFound)
}

func TestInboxAck_OutsideTheGuardIsRejectedAndKeepsTheBatch(t *testing.T) {
	store, box := inboxSessionWithPendingEvent(t, "other/inbox")
	taken, err := box.Take("other/inbox")
	if err != nil || taken == nil {
		t.Fatalf("take = %+v, %v", taken, err)
	}

	err = InboxAck(&config.Config{SessionGuard: "^acme/"}, store, "other/inbox", taken.ID)

	wantServiceError(t, err, ErrRepoNotAllowed)
	again, err := box.Take("other/inbox")
	if err != nil || again == nil || again.ID != taken.ID {
		t.Fatalf("take after a rejected ack = %+v (err %v), want batch %s again", again, err, taken.ID)
	}
}

func TestInboxAck_RemovesTheBatchOfASessionInsideTheGuard(t *testing.T) {
	store, box := inboxSessionWithPendingEvent(t, "acme/inbox")
	cfg := &config.Config{SessionGuard: "^acme/"}
	taken, err := InboxTake(cfg, store, "acme/inbox", 0)
	if err != nil || taken == nil {
		t.Fatalf("take = %+v, %v", taken, err)
	}

	if err := InboxAck(cfg, store, "acme/inbox", taken.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if pending, err := box.Pending("acme/inbox"); err != nil || pending {
		t.Fatalf("pending = %v (err %v), want an empty inbox", pending, err)
	}
}
