package dispatch

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func channelErrors(t *testing.T, log *eventlog.Store) []event.Event {
	t.Helper()
	errs, _, _, err := log.List("session-1", 0, event.Filter{Types: []string{event.TypeChannelError}})
	if err != nil {
		t.Fatal(err)
	}
	return errs
}

func appendInstruction(log *eventlog.Store, at time.Time) event.Event {
	ev, _, _, _ := log.Append(event.Event{SessionName: "session-1", Type: event.TypeInstruction, Body: "do it", Direction: event.Internal, Time: at})
	return ev
}

// awaitingRepair returns a switchable repair-wait predicate and its switch.
func awaitingRepair(initial bool) (func(string) (bool, error), *atomic.Bool) {
	var on atomic.Bool
	on.Store(initial)
	return func(string) (bool, error) { return on.Load(), nil }, &on
}

func TestDispatcherHold_EventAppendedWhileRuntimeDownIsDeliveredOnceAfterRepair(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "rt.sock")
	d, s := runtimeDispatcher(t, "session-1", log, path, "plect.instruction")
	fn, repairing := awaitingRepair(true)
	d.awaitingRepair = fn

	appendInstruction(log, time.Now())
	drainOnce(d, s)
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 0 {
		t.Fatalf("held event wrote plect.channel.error: %+v", errs)
	}
	if cur, _ := log.ReadCursor("session-1", dispatcherConsumer); cur != 0 {
		t.Fatalf("cursor advanced to %d while the event was held", cur)
	}
	if ch := d.state.Get("session-1").ChannelDeliveryHealth; ch != nil {
		t.Fatalf("a held delivery counted toward the failure streak: %+v", ch)
	}

	recv := startFakeSocketAt(t, path)
	repairing.Store(false)
	drainOnce(d, s)
	if typ := recvType(t, recv); typ != event.TypeInstruction {
		t.Fatalf("delivered type = %q", typ)
	}
	drainOnce(d, s)
	assertNoDelivery(t, recv)
	if errs := channelErrors(t, log); len(errs) != 0 {
		t.Fatalf("delivery after repair wrote plect.channel.error: %+v", errs)
	}
}

func TestDispatcherHold_ExpiredHoldWritesChannelErrorAndAdvances(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "rt.sock")
	d, s := runtimeDispatcher(t, "session-1", log, path, "plect.instruction")
	fn, _ := awaitingRepair(true)
	d.awaitingRepair = fn

	expired := appendInstruction(log, time.Now().Add(-repairHoldLimit-time.Minute))
	fresh := appendInstruction(log, time.Now())
	drainOnce(d, s)

	errs := channelErrors(t, log)
	if len(errs) != 1 || errs[0].Metadata["event_id"] != expired.ID {
		t.Fatalf("channel errors = %+v, want exactly one for the expired event", errs)
	}
	recv := startFakeSocketAt(t, path)
	drainOnce(d, s)
	if typ := recvType(t, recv); typ != event.TypeInstruction {
		t.Fatalf("delivered type = %q", typ)
	}
	if errs := channelErrors(t, log); len(errs) != 1 || errs[0].Metadata["event_id"] == fresh.ID {
		t.Fatalf("the event behind the expired one was dropped: %+v", errs)
	}
}

func TestDispatcherHold_StopsWhenRepairIsNoLongerExpected(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	path := filepath.Join(t.TempDir(), "rt.sock")
	d, s := runtimeDispatcher(t, "session-1", log, path, "plect.instruction")
	fn, repairing := awaitingRepair(true)
	d.awaitingRepair = fn

	orig := appendInstruction(log, time.Now())
	drainOnce(d, s)
	repairing.Store(false)
	drainOnce(d, s)

	errs := channelErrors(t, log)
	if len(errs) != 1 || errs[0].Metadata["event_id"] != orig.ID {
		t.Fatalf("channel errors = %+v, want one for the released event", errs)
	}
	if cur, _ := log.ReadCursor("session-1", dispatcherConsumer); cur == 0 {
		t.Fatal("cursor did not advance once repair was no longer expected")
	}
}

func TestDispatcherHold_NoPredicateKeepsImmediateChannelError(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, s := runtimeDispatcher(t, "session-1", log, filepath.Join(t.TempDir(), "absent.sock"), "plect.instruction")

	appendInstruction(log, time.Now())
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 1 {
		t.Fatalf("channel errors = %+v, want one", errs)
	}
	if cur, _ := log.ReadCursor("session-1", dispatcherConsumer); cur == 0 {
		t.Fatal("cursor did not advance")
	}
}

func TestDispatcherHold_PredicateFailureFallsBackToChannelError(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, s := runtimeDispatcher(t, "session-1", log, filepath.Join(t.TempDir(), "absent.sock"), "plect.instruction")
	d.awaitingRepair = func(string) (bool, error) { return false, errors.New("health unreadable") }

	appendInstruction(log, time.Now())
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 1 {
		t.Fatalf("channel errors = %+v, want one", errs)
	}
}

func TestDispatcherHold_ChannelInputFailureIsNeverHeld(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, s := runtimeDispatcher(t, "session-1", log, "", "plect.instruction")
	d.channels[0].Inputs = map[string]*lang.Value{"path": fromValue("nodes.absent.outputs.socket_path")}
	fn, _ := awaitingRepair(true)
	d.awaitingRepair = fn

	appendInstruction(log, time.Now())
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 1 {
		t.Fatalf("channel errors = %+v, want one: an unresolvable input is not a liveness failure", errs)
	}
}

func TestDispatcherHold_RetryDoesNotRedeliverToChannelsThatSucceeded(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	live, recv := startFakeSocket(t)
	dead := filepath.Join(t.TempDir(), "rt.sock")
	s := &domain.Session{Name: "session-1", Tasks: map[string]*contract.TaskState{}}
	st := state.NewStore(log.Dir())
	if err := st.Put(s); err != nil {
		t.Fatal(err)
	}
	fn, repairing := awaitingRepair(true)
	d := &sessionDispatcher{
		session: "session-1",
		channels: []config.EventChannel{
			{Name: "live", Uses: "sock", Inputs: map[string]*lang.Value{"path": literalValue(live)}, Include: []string{"plect.instruction"}},
			{Name: "dead", Uses: "sock", Inputs: map[string]*lang.Value{"path": literalValue(dead)}, Include: []string{"plect.instruction"}},
		},
		defs:           map[string]config.ChannelDefinition{"sock": socketChannel()},
		log:            log,
		state:          st,
		policy:         channel.RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Timeout: 200 * time.Millisecond},
		awaitingRepair: fn,
	}

	appendInstruction(log, time.Now())
	drainOnce(d, s)
	recvType(t, recv)
	drainOnce(d, s)
	assertNoDelivery(t, recv)

	deadRecv := startFakeSocketAt(t, dead)
	repairing.Store(false)
	drainOnce(d, s)
	recvType(t, deadRecv)
	assertNoDelivery(t, recv)
}

func TestDispatcherHold_TimeoutThatDoesNotResolveIsNeverHeld(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, s := runtimeDispatcher(t, "session-1", log, filepath.Join(t.TempDir(), "absent.sock"), "plect.instruction")
	for name, def := range d.defs {
		def.Timeout = literalValue("not-a-duration")
		d.defs[name] = def
	}
	fn, _ := awaitingRepair(true)
	d.awaitingRepair = fn

	appendInstruction(log, time.Now())
	drainOnce(d, s)

	if errs := channelErrors(t, log); len(errs) != 1 {
		t.Fatalf("channel errors = %+v, want one: a timeout that does not resolve fails before any delivery attempt", errs)
	}
	if cur, _ := log.ReadCursor("session-1", dispatcherConsumer); cur == 0 {
		t.Fatal("cursor did not advance past a failure repair cannot fix")
	}
}
