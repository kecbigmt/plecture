package adapter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestConnectionLoopScansReconnectAndGatesOnlineBoundary(t *testing.T) {
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1700000000, 0).UTC()
	key := "T1/UBOT/C1"
	if err := store.SetBoundary(key, base); err != nil {
		t.Fatal(err)
	}
	b := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}}, teamID: "T1", botUserID: "UBOT", recovery: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan socketmode.Event)
	ticks := make(chan time.Time)
	started := make(chan int, 2)
	release := make(chan struct{})
	count := 0
	scan := func(ctx context.Context) error {
		count++
		started <- count
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	go func() { b.runEvents(ctx, events, ticks, scan, nil); close(done) }()
	send := func(typ socketmode.EventType) {
		t.Helper()
		select {
		case events <- socketmode.Event{Type: typ}:
		case <-time.After(time.Second):
			t.Fatal("event loop stopped")
		}
	}
	tick := func(at time.Time) {
		t.Helper()
		for range 2 {
			select {
			case ticks <- at:
			case <-time.After(time.Second):
				t.Fatal("ticker stopped")
			}
		}
	}
	waitScan := func(want int) {
		t.Helper()
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("scan=%d want=%d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("scan not started")
		}
	}
	send(socketmode.EventTypeConnected)
	waitScan(1)
	tick(base.Add(time.Minute))
	if !store.Boundary(key).Equal(base) {
		t.Fatal("boundary advanced during scan")
	}
	send(socketmode.EventTypeDisconnect)
	send(socketmode.EventTypeConnected)
	release <- struct{}{}
	waitScan(2)
	tick(base.Add(2 * time.Minute))
	if !store.Boundary(key).Equal(base) {
		t.Fatal("boundary advanced during reconnect scan")
	}
	release <- struct{}{}
	deadline := time.After(time.Second)
	for !store.Boundary(key).Equal(base.Add(3 * time.Minute)) {
		select {
		case <-deadline:
			t.Fatal("online boundary did not advance after scan")
		default:
			tick(base.Add(3 * time.Minute))
		}
	}
	send(socketmode.EventTypeConnectionError)
	tick(base.Add(4 * time.Minute))
	if !store.Boundary(key).Equal(base.Add(3 * time.Minute)) {
		t.Fatal("boundary advanced while disconnected")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event loop did not stop")
	}
}

type gatedPublisher struct {
	started chan struct{}
	release chan struct{}
	calls   int
}

func (p *gatedPublisher) Publish(_ context.Context, _, _, _, _ string, _ map[string]string) (int, error) {
	p.calls++
	p.started <- struct{}{}
	<-p.release
	return 1, nil
}

func TestSocketEventAckFollowsHandlingAndResentMentionIsDeduplicated(t *testing.T) {
	p := &gatedPublisher{started: make(chan struct{}, 1), release: make(chan struct{})}
	b := testRoutingBot(nil)
	b.publisher = p
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan socketmode.Event, 2)
	acks := make(chan string, 2)
	done := make(chan struct{})
	go func() {
		b.runEvents(ctx, events, nil, func(context.Context) error { return nil }, func(req socketmode.Request) error {
			acks <- req.EnvelopeID
			return nil
		})
		close(done)
	}()
	event := socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: testMention()},
	}, Request: &socketmode.Request{EnvelopeID: "first"}}
	events <- event
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("mention handling did not start")
	}
	select {
	case id := <-acks:
		t.Fatalf("acknowledged %q before handling completed", id)
	default:
	}
	close(p.release)
	select {
	case id := <-acks:
		if id != "first" {
			t.Fatalf("acknowledged %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("first event was not acknowledged")
	}
	event.Request = &socketmode.Request{EnvelopeID: "retry"}
	events <- event
	select {
	case id := <-acks:
		if id != "retry" {
			t.Fatalf("acknowledged %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("retry was not acknowledged")
	}
	if p.calls != 1 {
		t.Fatalf("publish calls = %d, want 1", p.calls)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event loop did not stop")
	}
}
