package inbox

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
)

const testSession = "review-1"

type fixture struct {
	t     *testing.T
	log   *eventlog.Store
	inbox *Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	return fixture{t: t, log: eventlog.NewStore(dir), inbox: NewStore(dir)}
}

// handOver appends an event and hands it to the inbox, as the dispatcher does
// for an inbox binding.
func (f fixture) handOver(id, supersedes string) {
	f.t.Helper()
	_, seq, _, err := f.log.Append(event.Event{
		ID: id, SessionName: testSession, Type: event.TypeUserEmit,
		Source: event.SourceTick, Direction: event.Inbound, Supersedes: supersedes,
	})
	if err != nil {
		f.t.Fatalf("append %s: %v", id, err)
	}
	if err := f.inbox.Add(testSession, []int64{seq}, "runtime"); err != nil {
		f.t.Fatalf("hand over %s: %v", id, err)
	}
}

func (f fixture) take() *Batch {
	f.t.Helper()
	batch, err := f.inbox.Take(testSession)
	if err != nil {
		f.t.Fatalf("take: %v", err)
	}
	return batch
}

func ids(batch *Batch) []string {
	if batch == nil {
		return nil
	}
	out := make([]string, 0, len(batch.Events))
	for _, ev := range batch.Events {
		out = append(out, ev.ID)
	}
	return out
}

func equal(a, b []string) bool {
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

func TestSuperseded(t *testing.T) {
	ev := func(id, supersedes string) event.Event { return event.Event{ID: id, Supersedes: supersedes} }
	cases := []struct {
		name string
		evs  []event.Event
		want []string
	}{
		{"nothing names anything", []event.Event{ev("a", ""), ev("b", "")}, nil},
		{"a chain keeps only its newest", []event.Event{ev("a", ""), ev("b", "a"), ev("c", "b")}, []string{"a", "b"}},
		{"a reference to an absent event omits nothing", []event.Event{ev("b", "a"), ev("x", "")}, nil},
		{"an event naming itself is not omitted", []event.Event{ev("a", "a")}, nil},
		{"independent chains are judged separately", []event.Event{ev("a", ""), ev("p", ""), ev("b", "a"), ev("q", "p")}, []string{"a", "p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Superseded(tc.evs)
			if len(got) != len(tc.want) {
				t.Fatalf("Superseded = %v, want %v", got, tc.want)
			}
			for _, id := range tc.want {
				if !got[id] {
					t.Fatalf("Superseded = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestTake_ReturnsNothingForAnEmptyInbox(t *testing.T) {
	f := newFixture(t)
	f.handOver("seed", "")
	if err := f.inbox.Ack(testSession, f.take().ID); err != nil {
		t.Fatalf("ack seed: %v", err)
	}

	if batch := f.take(); batch != nil {
		t.Fatalf("take on an empty inbox = %+v, want nil", batch)
	}
}

func TestTake_DeliversPendingEventsTogetherWithSupersededOnesOmitted(t *testing.T) {
	f := newFixture(t)
	f.handOver("kick-1", "")
	f.handOver("message", "")
	f.handOver("kick-2", "kick-1")
	f.handOver("kick-3", "kick-2")

	batch := f.take()

	if got, want := ids(batch), []string{"message", "kick-3"}; !equal(got, want) {
		t.Fatalf("batch events = %v, want %v", got, want)
	}
	if batch.Deliveries != 1 {
		t.Fatalf("deliveries = %d, want 1", batch.Deliveries)
	}
}

func TestTake_RedeliversTheSameBatchUntilItIsAcknowledged(t *testing.T) {
	f := newFixture(t)
	f.handOver("first", "")
	taken := f.take()
	f.handOver("later", "")

	// The consumer crashed between take and ack: it takes again on restart.
	again := f.take()

	if again.ID != taken.ID || again.Deliveries != 2 {
		t.Fatalf("redelivery = batch %s delivered %d times, want batch %s delivered twice", again.ID, again.Deliveries, taken.ID)
	}
	if got, want := ids(again), []string{"first"}; !equal(got, want) {
		t.Fatalf("redelivered events = %v, want %v", got, want)
	}
}

func TestAck_ReleasesTheBatchSoTheNextTakeGetsLaterEvents(t *testing.T) {
	f := newFixture(t)
	f.handOver("first", "")
	taken := f.take()
	f.handOver("later", "")

	if err := f.inbox.Ack(testSession, taken.ID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	next := f.take()
	if next.ID == taken.ID || next.Deliveries != 1 {
		t.Fatalf("next batch = %+v, want a new batch delivered once", next)
	}
	if got, want := ids(next), []string{"later"}; !equal(got, want) {
		t.Fatalf("next batch events = %v, want %v", got, want)
	}
}

func TestAck_SupersededEntriesLeaveWithTheirBatch(t *testing.T) {
	f := newFixture(t)
	f.handOver("kick-1", "")
	f.handOver("kick-2", "kick-1")

	if err := f.inbox.Ack(testSession, f.take().ID); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if batch := f.take(); batch != nil {
		t.Fatalf("take after ack = %v, want nothing: the omitted entry must not come back", ids(batch))
	}
}

func TestAck_FailsForABatchThatIsNotOutstanding(t *testing.T) {
	f := newFixture(t)
	f.handOver("first", "")
	taken := f.take()
	if err := f.inbox.Ack(testSession, taken.ID); err != nil {
		t.Fatalf("first ack: %v", err)
	}

	if err := f.inbox.Ack(testSession, taken.ID); err == nil {
		t.Fatal("second ack of the same batch succeeded")
	}
	if err := f.inbox.Ack(testSession, "never-taken"); err == nil {
		t.Fatal("ack of an unknown batch succeeded")
	}
}

func TestTake_ParksABatchDeliveredThreeTimesAndMovesOn(t *testing.T) {
	f := newFixture(t)
	f.handOver("stale", "")
	f.handOver("poison", "stale")
	for i := 0; i < MaxDeliveries; i++ {
		f.take()
	}
	f.handOver("later", "")

	next := f.take()

	if got, want := ids(next), []string{"later"}; !equal(got, want) {
		t.Fatalf("batch after parking = %v, want %v", got, want)
	}
	errs, _, _, err := f.log.List(testSession, 0, event.Filter{Types: []string{event.TypeChannelError}})
	if err != nil {
		t.Fatalf("list channel errors: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("channel errors = %d, want one for the delivered event and none for the omitted one", len(errs))
	}
	md := errs[0].Metadata
	if md["event_id"] != "poison" || md["channel"] != "runtime" || md["attempts"] != "3" {
		t.Fatalf("channel error metadata = %v, want event_id=poison channel=runtime attempts=3", md)
	}
}

func TestPending_ReportsWhetherATakeWouldReturnABatch(t *testing.T) {
	f := newFixture(t)
	f.handOver("seed", "")
	if pending, err := f.inbox.Pending(testSession); err != nil || !pending {
		t.Fatalf("pending with one entry = (%v, %v), want (true, nil)", pending, err)
	}
	if err := f.inbox.Ack(testSession, f.take().ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if pending, err := f.inbox.Pending(testSession); err != nil || pending {
		t.Fatalf("pending after ack = (%v, %v), want (false, nil)", pending, err)
	}
}

func TestTake_UnknownSessionIsAnError(t *testing.T) {
	f := newFixture(t)
	if _, err := f.inbox.Take("no-such-session"); err == nil {
		t.Fatal("take on an unknown session succeeded")
	}
}
