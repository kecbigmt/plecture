package adapter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

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
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, teamID: "T1", botUserID: "UBOT", recovery: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
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
	go func() { b.runEvents(ctx, events, ticks, scan); close(done) }()
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
