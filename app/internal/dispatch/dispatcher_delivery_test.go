package dispatch

import (
	"log/slog"
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

// runtimeOwnsActivity marks every node of the fixture session as declaring
// an activity probe, which makes the binding that reads it the runtime's.
func runtimeOwnsActivity(d *sessionDispatcher, s *domain.Session) {
	d.runtimeNodes = map[string]bool{}
	for id := range domain.MergedTasks(s) {
		d.runtimeNodes[id] = true
	}
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
	runtimeOwnsActivity(d, s)
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
	runtimeOwnsActivity(d, s)
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

func TestDispatcher_DeliveryToANodeWithoutActivityIsNotRecorded(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction", "user.emit")
	SeedCursor(d.log, d.session)

	appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none: a binding that carries instructions to a node without activity is a mirror", rec)
	}
}

func TestDispatcher_MirrorDeliveryIsNotRecordedWhenTheRuntimeSkipsTheEvent(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	sock, recv := startFakeSocket(t)
	mirrorSock, mirrorRecv := startFakeSocket(t)
	d, s := runtimeDispatcher(t, deliverySession, log, sock, "plect.instruction")
	runtimeOwnsActivity(d, s)
	d.channels = append(d.channels, config.EventChannel{Name: "mirror", Uses: d.channels[0].Uses, Inputs: map[string]*lang.Value{"path": literalValue(mirrorSock)}, Include: []string{"plect.instruction", "user.emit"}})
	SeedCursor(d.log, d.session)

	appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, mirrorRecv)
	assertNoDelivery(t, recv)

	if rec := unansweredDelivery(t, d); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none: only the mirror received the event", rec)
	}
}

func TestReachesRuntime(t *testing.T) {
	terminalArg := &lang.Value{Form: lang.FormTerminal, Terminal: "send_text"}
	defs := map[string]config.ChannelDefinition{
		"socket":   socketChannel(),
		"inbox":    {Type: config.ChannelTypeInbox},
		"terminal": {Type: config.ChannelTypeShell, Action: &lang.Action{Type: lang.ActionShell, Script: "true", Bind: map[string]*lang.Value{"send": terminalArg}}},
	}
	reads := func(node string) map[string]*lang.Value {
		return map[string]*lang.Value{"path": fromValue("nodes." + node + ".outputs.socket_path")}
	}
	cases := []struct {
		name     string
		ch       config.EventChannel
		terminal string
		runtime  map[string]bool
		want     bool
	}{
		{"input reads the activity node", config.EventChannel{Uses: "socket", Inputs: reads("agent")}, "", map[string]bool{"agent": true}, true},
		{"input reads another node", config.EventChannel{Uses: "socket", Inputs: reads("thread")}, "", map[string]bool{"agent": true}, false},
		{"literal input", config.EventChannel{Uses: "socket", Inputs: map[string]*lang.Value{"path": literalValue("/tmp/x.sock")}}, "", map[string]bool{"agent": true}, false},
		{"terminal verb of the activity node", config.EventChannel{Uses: "terminal"}, "pane", map[string]bool{"pane": true}, true},
		{"terminal verb of a node without activity", config.EventChannel{Uses: "terminal"}, "pane", map[string]bool{"agent": true}, false},
		{"inbox with an activity node", config.EventChannel{Uses: "inbox"}, "", map[string]bool{"agent": true}, true},
		{"inbox without one", config.EventChannel{Uses: "inbox"}, "", nil, false},
		{"unknown definition", config.EventChannel{Uses: "missing", Inputs: reads("agent")}, "", map[string]bool{"agent": true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &sessionDispatcher{defs: defs, runtimeNodes: tc.runtime}
			if tc.terminal != "" {
				d.terminal = &terminalOwner{NodeID: tc.terminal}
			}
			if got := d.reachesRuntime(tc.ch); got != tc.want {
				t.Fatalf("reachesRuntime = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDispatcher_FailedRuntimeDeliveryLeavesNoRecord(t *testing.T) {
	log := eventlog.NewStore(t.TempDir())
	dead := filepath.Join(t.TempDir(), "absent.sock")
	d, s := runtimeDispatcher(t, deliverySession, log, dead, "plect.instruction", "user.emit")
	runtimeOwnsActivity(d, s)
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
	runtimeOwnsActivity(d, s)
	d.channels = append(d.channels, config.EventChannel{Name: "mirror", Uses: d.channels[0].Uses, Inputs: map[string]*lang.Value{"path": literalValue(dead)}, Include: []string{"user.emit"}})
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
	s, err := d.state.GetE(deliverySession)
	if err != nil {
		t.Fatal(err)
	}
	runtimeOwnsActivity(d, s)

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
	runtimeOwnsActivity(d, s)
	SeedCursor(d.log, d.session)
	previousRun := time.Now().Add(-time.Hour)
	if err := d.state.RecordUnansweredDelivery(deliverySession, domain.UnansweredDelivery{EventID: "old", EventType: event.TypeUserEmit, DeliveredAt: previousRun}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, task := range s.Tasks {
		task.SetupAt = previousRun.Add(30 * time.Minute)
	}

	ev := appendUserEmit(t, log, "emit-1")
	drainOnce(d, s)
	recvType(t, recv)

	if rec := unansweredDelivery(t, d); rec == nil || rec.EventID != ev.ID {
		t.Fatalf("unanswered delivery = %+v, want %s to replace the previous run's record", rec, ev.ID)
	}
}

func TestResolveRuntimeNodes_NamesRunScopedNodesThatDeclareActivity(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	globalDir := filepath.Join(tmpHome, ".config", "plect")
	writeFile(t, filepath.Join(globalDir, "config.toml"), "schema_version = 2\n")
	effect := func(id, scope, health string) string {
		return "[" + id + "]\nkind = \"effect\"\nscope = \"" + scope + "\"\n\n[" + id + ".setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n\n" + health
	}
	activity := func(id string) string {
		return "[" + id + ".health.activity]\ntype = \"shell\"\nscript = \"true\"\n"
	}
	alive := func(id string) string { return "[" + id + ".health.alive]\ntype = \"noop\"\n" }
	writeFile(t, filepath.Join(globalDir, "tasks", "agent.toml"), effect("agent", "run", alive("agent")+activity("agent")))
	writeFile(t, filepath.Join(globalDir, "tasks", "sidecar.toml"), effect("sidecar", "run", alive("sidecar")))
	writeFile(t, filepath.Join(globalDir, "tasks", "thread.toml"), effect("thread", "session", alive("thread")+activity("thread")))
	writeFile(t, filepath.Join(globalDir, "workflows", "coding.toml"), `
[coding]
kind = "workflow"
resource = "issue"
[[coding.nodes]]
uses = "agent"
[[coding.nodes]]
uses = "sidecar"
[[coding.nodes]]
uses = "thread"
`)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	s := terminalRevisionSession(t, cfg)

	got := resolveRuntimeNodes(slog.Default(), cfg, s, workflows["coding"])
	if len(got) != 1 || !got["agent"] {
		t.Fatalf("runtime nodes = %v, want only the run-scoped node that declares activity", got)
	}
}
