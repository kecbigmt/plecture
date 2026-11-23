package dispatch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func recvBody(t *testing.T, recv <-chan protocol.MessagePayload) string {
	t.Helper()
	select {
	case pl := <-recv:
		var ev event.Event
		if err := json.Unmarshal([]byte(pl.Text), &ev); err != nil {
			t.Fatalf("payload is not event json: %v", err)
		}
		return ev.Body
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery received")
		return ""
	}
}

func setRuntimeNode(t *testing.T, store *state.Store, name string, status, socket string) {
	t.Helper()
	if err := store.Update(name, func(s *domain.Session) error {
		s.Nodes["agent"] = &contract.TaskState{Scope: contract.TaskScopeRun, Status: status, Outputs: map[string]any{"socket_path": socket}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Given a session whose runtime goes to sleep, when inbound events arrive
// while it is down — the one that wakes it and others queued behind it — then
// the woken runtime receives each of them once, in arrival order.
func TestSupervisor_EventsArrivingWhileAsleepReachTheWokenRuntimeOnceInOrder(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(globalDir, "channels", "agent_channel.toml"), `
[agent_channel]
kind = "channel"
type = "unix_socket"
path = { from = "inputs.path" }
body = { json = { from = "event" } }

[agent_channel.input_schema]
path = { type = "string", required = true }
`)
	writeFile(t, filepath.Join(globalDir, "tasks", "agent.toml"), `
[agent]
kind  = "effect"
scope = "run"
`)
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"
[[coding.nodes]]
id   = "agent"
uses = "agent"
[[coding.event.channel]]
name        = "runtime"
uses        = "agent_channel"
inputs.path = { from = "nodes.agent.outputs.socket_path" }
include     = ["user.emit"]
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	const name = "sleeper"
	store := state.NewStore(t.TempDir())
	firstSock, firstRecv := startFakeSocket(t)
	putRevisionSession(t, cfg, store, &domain.Session{Name: name, Workflow: "coding", Nodes: map[string]*contract.TaskState{
		"agent": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{"socket_path": firstSock}},
	}})

	log := eventlog.NewStore(store.Dir())
	hub := sessionhub.NewRegistry(log)
	defer hub.Close()
	sup := NewSupervisor(func() *config.Config { return cfg }, store, log, hub)
	active := map[string]context.CancelFunc{}
	skip := map[string]bool{}
	var wg sync.WaitGroup
	defer func() {
		for _, cancel := range active {
			cancel()
		}
		wg.Wait()
	}()
	emit := func(body string) string {
		stored, _, _, err := log.Append(event.Event{SessionName: name, Type: "user.emit", Direction: event.Inbound, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return stored.ID
	}
	// A dispatcher cancelled between a delivery and its cursor commit replays
	// that event by design, so each step waits for the commit first.
	caughtUp := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, _, end, err := log.List(name, 0, event.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if cur, err := log.ReadCursor(name, dispatcherConsumer); err == nil && cur == end {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("dispatcher never caught up with the log")
			}
			time.Sleep(time.Millisecond)
		}
	}

	sup.reconcile(t.Context(), active, skip, &wg)
	deadline := time.Now().Add(2 * time.Second)
	for !log.HasCursor(name, dispatcherConsumer) {
		if time.Now().After(deadline) {
			t.Fatal("dispatcher never seeded its cursor")
		}
		time.Sleep(time.Millisecond)
	}
	emit("before sleep")
	if got := recvBody(t, firstRecv); got != "before sleep" {
		t.Fatalf("first-run delivery = %q", got)
	}
	caughtUp()

	setRuntimeNode(t, store, name, contract.TaskStatusCleaned, firstSock)
	sup.reconcile(t.Context(), active, skip, &wg)
	wg.Wait()
	if len(active) != 0 {
		t.Fatalf("dispatcher still active while asleep: %d", len(active))
	}
	want := []string{emit("waking event"), emit("queued behind it")}

	woken := startOrderedPane(t)
	setRuntimeNode(t, store, name, contract.TaskStatusProduced, woken.path)
	sup.reconcile(t.Context(), active, skip, &wg)
	caughtUp()
	if got := woken.deliveredIDs(t); !sameIDs(got, want) {
		t.Fatalf("woken runtime received %v, want %v", got, want)
	}

	sup.reconcile(t.Context(), active, skip, &wg)
	if _, _, _, err := log.Append(event.Event{SessionName: name, Type: event.TypeNodeResult, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}
	caughtUp()
	if got := woken.deliveredIDs(t); len(got) != 0 {
		t.Fatalf("redelivered after the wake: %v", got)
	}
	assertNoDelivery(t, firstRecv)
}
