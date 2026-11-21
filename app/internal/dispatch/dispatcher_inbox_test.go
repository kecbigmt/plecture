package dispatch

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/inbox"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/sockettest"
	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/contracts/event"
)

const inboxSession = "review-1"

func appendKick(t *testing.T, log *eventlog.Store, id, supersedes string) {
	t.Helper()
	if _, _, _, err := log.Append(event.Event{
		ID: id, SessionName: inboxSession, Type: event.TypeUserEmit, Source: event.SourceTick,
		Direction: event.Outbound, Supersedes: supersedes,
	}); err != nil {
		t.Fatalf("append %s: %v", id, err)
	}
}

// orderedPane is a fake pane that reads one connection at a time. A reader per
// connection would let two deliveries overtake each other on their way to the
// test, and the order of deliveries is what these tests specify.
type orderedPane struct {
	path string
	recv chan *protocol.MessagePayload
}

func startOrderedPane(t *testing.T) *orderedPane {
	t.Helper()
	path := filepath.Join(sockettest.Dir(t), "c.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p := &orderedPane{path: path, recv: make(chan *protocol.MessagePayload, 16)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			pl, ok := readFakeMessage(conn)
			conn.Close()
			if ok {
				p.recv <- &pl
			} else {
				p.recv <- nil
			}
		}
	}()
	return p
}

// deliveredIDs returns every delivery made before the call, in order. It
// marks the end with a connection of its own that carries no message: the
// pane reads connections in the order they were made, so nothing delivered
// earlier can arrive after that mark, and no wait has to guess when the pane
// has caught up.
func (p *orderedPane) deliveredIDs(t *testing.T) []string {
	t.Helper()
	mark, err := net.Dial("unix", p.path)
	if err != nil {
		t.Fatalf("dial the fake pane: %v", err)
	}
	mark.Close()
	var ids []string
	for {
		select {
		case pl := <-p.recv:
			if pl == nil {
				return ids
			}
			var ev struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(pl.Text), &ev); err != nil {
				t.Fatalf("payload is not event json: %v", err)
			}
			ids = append(ids, ev.ID)
		case <-time.After(10 * time.Second):
			t.Fatalf("the fake pane never reached the end mark; got %v", ids)
		}
	}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDispatcher_PaneReceivesOnlyTheLatestOfKicksThatPiledUpWhileDown(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	pane := startOrderedPane(t)
	d, s := runtimeDispatcher(t, inboxSession, log, pane.path, "user.emit")
	SeedCursor(d.log, d.session)

	// The session is down: nothing drains while these arrive.
	appendKick(t, log, "kick-1", "")
	appendKick(t, log, "kick-2", "kick-1")
	appendKick(t, log, "kick-3", "kick-2")

	drainOnce(d, s)

	if got, want := pane.deliveredIDs(t), []string{"kick-3"}; !sameIDs(got, want) {
		t.Fatalf("pane deliveries = %v, want %v", got, want)
	}
	cur, _ := log.ReadCursor(inboxSession, dispatcherConsumer)
	_, _, end, _ := log.List(inboxSession, 0, event.Filter{})
	if cur != end {
		t.Fatalf("delivery position = %d, want %d: an omitted event is processed, not left pending", cur, end)
	}
}

func TestDispatcher_PaneStillReceivesEveryEventNothingSupersedes(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	pane := startOrderedPane(t)
	d, s := runtimeDispatcher(t, inboxSession, log, pane.path, "user.emit")
	SeedCursor(d.log, d.session)

	appendKick(t, log, "kick-1", "")
	appendKick(t, log, "message", "")
	appendKick(t, log, "kick-2", "kick-1")

	drainOnce(d, s)

	if got, want := pane.deliveredIDs(t), []string{"message", "kick-2"}; !sameIDs(got, want) {
		t.Fatalf("pane deliveries = %v, want %v", got, want)
	}
}

func TestDispatcher_SupersessionIsJudgedPerBinding(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	pane := startOrderedPane(t)
	// This binding selects the older event's type only, so the event that
	// supersedes it is not pending for it.
	d, s := runtimeDispatcher(t, inboxSession, log, pane.path, "plect.instruction")
	SeedCursor(d.log, d.session)

	if _, _, _, err := log.Append(event.Event{ID: "old", SessionName: inboxSession, Type: event.TypeInstruction, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}
	appendKick(t, log, "new", "old")

	drainOnce(d, s)

	if got, want := pane.deliveredIDs(t), []string{"old"}; !sameIDs(got, want) {
		t.Fatalf("deliveries = %v, want %v", got, want)
	}
}

func inboxDispatcher(t *testing.T, log *eventlog.Store) (*sessionDispatcher, *inbox.Store) {
	t.Helper()
	d, _ := runtimeDispatcher(t, inboxSession, log, "", "user.emit")
	d.channels = []config.EventChannel{{Name: "runtime", Uses: "runtime_inbox", Include: []string{"user.emit"}}}
	d.defs = map[string]config.ChannelDefinition{"runtime_inbox": {Type: config.ChannelTypeInbox}}
	d.inbox = inbox.NewStore(log.Dir())
	SeedCursor(d.log, d.session)
	return d, d.inbox
}

func takeIDs(t *testing.T, store *inbox.Store) (string, []string) {
	t.Helper()
	batch, err := store.Take(inboxSession)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if batch == nil {
		return "", nil
	}
	ids := make([]string, 0, len(batch.Events))
	for _, ev := range batch.Events {
		ids = append(ids, ev.ID)
	}
	return batch.ID, ids
}

func TestDispatcher_TakingRuntimeGetsOneBatchWithOnlyTheLatestKickAfterDown(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, store := inboxDispatcher(t, log)
	s, _ := d.state.GetE(inboxSession)

	appendKick(t, log, "kick-1", "")
	appendKick(t, log, "kick-2", "kick-1")
	appendKick(t, log, "kick-3", "kick-2")

	drainOnce(d, s)

	batch, ids := takeIDs(t, store)
	if want := []string{"kick-3"}; !sameIDs(ids, want) {
		t.Fatalf("first batch = %v, want %v", ids, want)
	}
	if err := store.Ack(inboxSession, batch); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if _, rest := takeIDs(t, store); rest != nil {
		t.Fatalf("second batch = %v, want nothing", rest)
	}
}

func TestDispatcher_InboxBindingReceivesOnlyEventsItsIncludeSelects(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, store := inboxDispatcher(t, log)
	s, _ := d.state.GetE(inboxSession)

	if _, _, _, err := log.Append(event.Event{ID: "status", SessionName: inboxSession, Type: event.TypeStatusMessage, Direction: event.Outbound}); err != nil {
		t.Fatal(err)
	}
	appendKick(t, log, "kick", "")

	drainOnce(d, s)

	if _, ids := takeIDs(t, store); !sameIDs(ids, []string{"kick"}) {
		t.Fatalf("batch = %v, want [kick]", ids)
	}
}

func TestDispatcher_DoesNotHandAnAcknowledgedEventOverAgainWhileAPushDeliveryIsHeld(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, store := inboxDispatcher(t, log)
	// A second binding whose runtime is unreachable holds the delivery
	// position on the same event, so every drain lists that event again.
	d.channels = append(d.channels, config.EventChannel{Name: "pane", Uses: "claude_channel", Inputs: map[string]*lang.Value{"path": literalValue(filepath.Join(t.TempDir(), "dead.sock"))}, Include: []string{"user.emit"}})
	d.defs["claude_channel"] = socketChannel()
	d.awaitingRepair = func(string) (bool, error) { return true, nil }
	s, _ := d.state.GetE(inboxSession)

	appendKick(t, log, "kick", "")
	drainOnce(d, s)
	batch, ids := takeIDs(t, store)
	if !sameIDs(ids, []string{"kick"}) {
		t.Fatalf("batch = %v, want [kick]", ids)
	}
	if err := store.Ack(inboxSession, batch); err != nil {
		t.Fatalf("ack: %v", err)
	}

	drainOnce(d, s)

	if _, again := takeIDs(t, store); again != nil {
		t.Fatalf("batch after a second drain = %v, want nothing", again)
	}
}

func TestDispatcher_NeverHandsAChannelErrorBackToTheBindingItRecords(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, store := inboxDispatcher(t, log)
	d.channels[0].Include = []string{"*"}
	s, _ := d.state.GetE(inboxSession)

	if _, _, _, err := log.Append(event.Event{
		ID: "own-error", SessionName: inboxSession, Type: event.TypeChannelError, Direction: event.Internal,
		Metadata: map[string]string{"channel": "runtime", "event_type": event.TypeUserEmit},
	}); err != nil {
		t.Fatal(err)
	}

	drainOnce(d, s)

	if _, ids := takeIDs(t, store); ids != nil {
		t.Fatalf("batch = %v, want nothing: a parked batch's error would otherwise park again forever", ids)
	}
}

func TestDispatcher_HandsOverARecreatedSessionsEventsFromItsOwnStart(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	d, store := inboxDispatcher(t, log)
	s, _ := d.state.GetE(inboxSession)
	// The dispatcher outlived a destroy and recreate: it remembers a mark from
	// the earlier incarnation, whose sequences ran higher than the new log's.
	d.handedOver = 1000
	staleGen := "an-earlier-incarnation"

	appendKick(t, log, "kick", "")
	d.drain(context.Background(), s, &staleGen)

	if _, ids := takeIDs(t, store); !sameIDs(ids, []string{"kick"}) {
		t.Fatalf("batch = %v, want [kick]", ids)
	}
}
