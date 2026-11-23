package dispatch

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/contracts/event"
)

const deliverySession = "review-1"

func appendUserEmit(t *testing.T, log *eventlog.Store, id string) event.Event {
	t.Helper()
	ev, _, _, err := log.Append(event.Event{ID: id, SessionName: deliverySession, Type: event.TypeUserEmit, Direction: event.Inbound, Summary: "please continue"})
	if err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
	return ev
}

func unansweredDelivery(t *testing.T, d *sessionDispatcher) *domain.UnansweredDelivery {
	t.Helper()
	rec, err := d.state.UnansweredDelivery(deliverySession)
	if err != nil {
		t.Fatalf("read unanswered delivery: %v", err)
	}
	return rec
}

func TestDispatcher_RuntimeDeliveryIsRecordedAsUnanswered(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction", "user.emit")
	SeedCursor(d.log, d.session)
	before := time.Now()

	ev := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	rec := unansweredDelivery(t, d)
	if rec == nil || rec.EventID != ev.ID || rec.EventType != event.TypeUserEmit {
		t.Fatalf("unanswered delivery = %+v, want %s (user.emit)", rec, ev.ID)
	}
	if rec.DeliveredAt.Before(before) || rec.DeliveredAt.After(time.Now()) {
		t.Fatalf("delivered at %s, want during the drain (after %s)", rec.DeliveredAt, before)
	}
}

func TestDispatcher_LaterDeliveryKeepsTheEarliestUnanswered(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction", "user.emit")
	SeedCursor(d.log, d.session)

	first := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)
	appendUserEmit(t, log, "emit-2")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec == nil || rec.EventID != first.ID {
		t.Fatalf("unanswered delivery = %+v, want the first delivery %s", rec, first.ID)
	}
}

func TestDispatcher_DeliveryOnABindingThatCarriesNoInstructionsIsNotRecorded(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "user.emit")
	SeedCursor(d.log, d.session)

	appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none: a binding that never carries instructions is a mirror, not the runtime", rec)
	}
}

func TestDispatcher_FailedRuntimeDeliveryLeavesNoRecord(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	dead := filepath.Join(t.TempDir(), "absent.sock")
	d, s := runtimeDispatcher(t, deliverySession, log, dead, "plect.instruction", "user.emit")
	SeedCursor(d.log, d.session)

	appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)

	if rec := unansweredDelivery(t, d); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none: the runtime never received the event", rec)
	}
}

func TestDispatcher_RuntimeDeliveryIsRecordedWhenAMirrorFails(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	dead := filepath.Join(t.TempDir(), "absent.sock")
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction", "user.emit")
	d.channels = append(d.channels, config.EventChannel{Name: "mirror", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue(dead)}, Include: []string{"user.emit"}})
	SeedCursor(d.log, d.session)

	ev := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec == nil || rec.EventID != ev.ID {
		t.Fatalf("unanswered delivery = %+v, want %s: the runtime received it", rec, ev.ID)
	}
}

func TestDispatcher_InboxHandOverIsRecordedAsUnanswered(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, _ := inboxDispatcher(t, log)
	d.channels[0].Include = []string{"plect.instruction", "user.emit"}
	s, _ := d.state.GetE(deliverySession)

	ev := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)

	if rec := unansweredDelivery(t, d); rec == nil || rec.EventID != ev.ID || rec.EventType != event.TypeUserEmit {
		t.Fatalf("unanswered delivery = %+v, want %s handed to the inbox", rec, ev.ID)
	}
}

func TestDispatcher_RecordReplacesOneFromBeforeTheCurrentRun(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction", "user.emit")
	SeedCursor(d.log, d.session)
	previousRun := time.Now().Add(-time.Hour)
	if err := d.state.RecordUnansweredDelivery(deliverySession, domain.UnansweredDelivery{EventID: "old", EventType: event.TypeUserEmit, DeliveredAt: previousRun}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	s.Tasks["claude"].SetupAt = previousRun.Add(30 * time.Minute)

	ev := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec == nil || rec.EventID != ev.ID {
		t.Fatalf("unanswered delivery = %+v, want %s to replace the previous run's record", rec, ev.ID)
	}
}
