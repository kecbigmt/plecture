package channel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	protocol "github.com/kecbigmt/plect/contracts/channel-protocol"
	"github.com/kecbigmt/plect/contracts/event"
)

// TestShippedChannelDefsRender renders every shipped channel definition's
// template fields against a sample event, so a typo in config/tws/channels (e.g.
// the slack_thread curl -d JSON body) fails CI, not only at delivery.
func TestShippedChannelDefsRender(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	cfgDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..", "config", "tws")
	if _, err := os.Stat(cfgDir); err != nil {
		t.Skipf("shipped config not found at %s: %v", cfgDir, err)
	}
	channels, err := (&config.Config{BaseDir: cfgDir}).LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped): %v", err)
	}
	rctx := newRenderContext(
		map[string]any{"path": "/run/c.sock", "session": "o/r-1", "thread_ts": "12.34", "channel_id": "C0", "queue_dir": "/run/codex-exec/o-r-1/queue"},
		event.Event{Type: "github.ci_status", Summary: "CI failed: test"},
	)
	for id, def := range channels {
		fields := map[string]string{"path": def.Path, "body": def.Body, "command": def.Command}
		for i, a := range def.Args {
			fields[fmt.Sprintf("args[%d]", i)] = a
		}
		for name, tmpl := range fields {
			if tmpl == "" {
				continue
			}
			if _, rerr := renderField(name, tmpl, rctx); rerr != nil {
				t.Errorf("channel %q %s render: %v", id, name, rerr)
			}
		}
	}
	// Exercise both branches of the slack curl -d body (summary fallback and a
	// body with characters that must be JSON-escaped) so a template/escaping
	// regression fails CI.
	slack, ok := channels["slack_thread"]
	if !ok || len(slack.Args) == 0 {
		t.Fatal("shipped slack_thread channel missing or has no args")
	}
	body := slack.Args[len(slack.Args)-1] // the -d JSON payload is the final arg
	for _, tc := range []struct {
		ev   event.Event
		want string
	}{
		{event.Event{Type: "github.ci_status", Summary: "CI failed: test"}, "CI failed: test"},
		{event.Event{Type: event.TypeUserEmit, Summary: "s", Body: "kick \"now\"\nplease"}, "kick \"now\"\nplease"},
	} {
		rc := newRenderContext(map[string]any{"thread_ts": "12.34", "channel_id": "C0"}, tc.ev)
		got, err := renderField("-d", body, rc)
		if err != nil {
			t.Fatalf("slack -d render: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Errorf("slack -d body is not valid JSON: %v (%s)", err, got)
		} else if parsed["text"] != tc.want {
			t.Errorf("slack -d text = %q, want %q", parsed["text"], tc.want)
		}
	}
}

func readFramed(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	data := make([]byte, binary.BigEndian.Uint32(header))
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

func TestDeliver_UnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type result struct {
		data []byte
		err  error
	}
	got := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- result{err: err}
			return
		}
		defer conn.Close()
		data, err := readFramed(conn)
		got <- result{data: data, err: err}
	}()

	def := config.ChannelDefinition{Type: config.ChannelTypeUnixSocket, Path: sock, Body: "{{ json .Event }}"}
	ev := event.Event{ID: "01ABC", SessionName: "o/r-1", Type: event.TypeInstruction, Body: "resolve #1"}
	if err := Deliver(context.Background(), def, nil, ev); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	r := <-got
	if r.err != nil {
		t.Fatal(r.err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(r.data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Type != protocol.MsgMessage {
		t.Errorf("envelope type = %q, want %q", env.Type, protocol.MsgMessage)
	}
	var pl protocol.MessagePayload
	if err := env.UnmarshalPayload(&pl); err != nil {
		t.Fatal(err)
	}
	if pl.Source != "" {
		t.Errorf("Source = %q, want empty (content events must not look like a verdict)", pl.Source)
	}
	// Text is the rendered body = the event as JSON.
	var sent map[string]any
	if err := json.Unmarshal([]byte(pl.Text), &sent); err != nil {
		t.Fatalf("payload text is not the event JSON: %v (text=%q)", err, pl.Text)
	}
	if sent["type"] != event.TypeInstruction || sent["body"] != "resolve #1" {
		t.Errorf("delivered event = %+v", sent)
	}
}

func TestDeliver_Exec(t *testing.T) {
	dir := t.TempDir()
	def := config.ChannelDefinition{
		Type:    config.ChannelTypeExec,
		Command: "touch",
		Args:    []string{"{{.Inputs.dir}}/{{.Event.type}}"},
	}
	ev := event.Event{Type: event.TypeInstruction}
	if err := Deliver(context.Background(), def, map[string]any{"dir": dir}, ev); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	// argv rendered from both .Inputs and .Event, run without a shell.
	if _, err := os.Stat(filepath.Join(dir, event.TypeInstruction)); err != nil {
		t.Errorf("expected touched file: %v", err)
	}
}

// fakeExecutor records the argv it receives and returns a canned result, so
// tests can assert routing without shelling out.
type fakeExecutor struct {
	argv   [][]string
	stdout []byte
	stderr []byte
	err    error
}

func (f *fakeExecutor) Run(argv []string) (stdout, stderr []byte, err error) {
	f.argv = append(f.argv, argv)
	return f.stdout, f.stderr, f.err
}

func TestDeliverWithExecutor_EnvironmentExecutionRoutesThroughExecutor(t *testing.T) {
	def := config.ChannelDefinition{
		Type:      config.ChannelTypeExec,
		Command:   "tmux",
		Args:      []string{"send-keys", "{{.Event.type}}"},
		Execution: config.ExecutionEnvironment,
	}
	ev := event.Event{Type: event.TypeInstruction}
	ex := &fakeExecutor{}
	if err := DeliverWithExecutor(context.Background(), def, nil, ev, ex); err != nil {
		t.Fatalf("DeliverWithExecutor: %v", err)
	}
	if len(ex.argv) != 1 {
		t.Fatalf("argv calls = %d, want 1: %+v", len(ex.argv), ex.argv)
	}
	want := []string{"tmux", "send-keys", event.TypeInstruction}
	if !reflect.DeepEqual(ex.argv[0], want) {
		t.Errorf("argv = %+v, want %+v", ex.argv[0], want)
	}
}

func TestDeliverWithExecutor_HostExecutionIgnoresExecutor(t *testing.T) {
	// Execution unset (host default): even with a non-nil executor supplied,
	// delivery must run on the host exactly as Deliver always has.
	dir := t.TempDir()
	def := config.ChannelDefinition{
		Type:    config.ChannelTypeExec,
		Command: "touch",
		Args:    []string{"{{.Inputs.dir}}/{{.Event.type}}"},
	}
	ev := event.Event{Type: event.TypeInstruction}
	ex := &fakeExecutor{}
	if err := DeliverWithExecutor(context.Background(), def, map[string]any{"dir": dir}, ev, ex); err != nil {
		t.Fatalf("DeliverWithExecutor: %v", err)
	}
	if len(ex.argv) != 0 {
		t.Errorf("executor should not run for a host-plane channel, got %+v", ex.argv)
	}
	if _, err := os.Stat(filepath.Join(dir, event.TypeInstruction)); err != nil {
		t.Errorf("expected touched file (host execution): %v", err)
	}
}

func TestDeliverWithExecutor_NilExecutorFailsClosed(t *testing.T) {
	// Execution=="environment" but no executor supplied (e.g. environment
	// resolution failed) must fail closed — it must NEVER silently deliver on
	// the host, since the channel explicitly asked for the environment plane.
	dir := t.TempDir()
	def := config.ChannelDefinition{
		Type:      config.ChannelTypeExec,
		Command:   "touch",
		Args:      []string{"{{.Inputs.dir}}/{{.Event.type}}"},
		Execution: config.ExecutionEnvironment,
	}
	ev := event.Event{Type: event.TypeInstruction}
	err := DeliverWithExecutor(context.Background(), def, map[string]any{"dir": dir}, ev, nil)
	if err == nil {
		t.Fatal("expected fail-closed error, got nil")
	}
	if !strings.Contains(err.Error(), "environment executor") {
		t.Errorf("unexpected message: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, event.TypeInstruction)); err == nil {
		t.Error("must not have delivered on host")
	}
}

func TestDeliver_EmptyOptionalFieldRendersEmpty(t *testing.T) {
	dir := t.TempDir()
	// An event whose optional fields (body) are unset must still
	// deliver — {{.Event.body}} renders empty, not an error.
	def := config.ChannelDefinition{
		Type:    config.ChannelTypeExec,
		Command: "touch",
		Args:    []string{"{{.Inputs.dir}}/got-{{.Event.body}}"},
	}
	ev := event.Event{Type: "github.state", Summary: "CI failed"} // Body empty
	if err := Deliver(context.Background(), def, map[string]any{"dir": dir}, ev); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "got-")); err != nil {
		t.Errorf("expected file from empty-field render: %v", err)
	}
}

func TestRenderField_OptionalEventFieldEmpty(t *testing.T) {
	// The shipped tmux_send_keys.toml arg pattern: fall back to .summary when
	// .body is empty (github.* events carry their text in .summary), and append
	// the resource URL from metadata so a multi-PR session knows which one
	// changed. `with index` keeps the URL absent for events without one, and an
	// all-empty event must still render without failing under missingkey=error.
	const arg = `[{{.Event.type}}] {{if .Event.body}}{{.Event.body}}{{else}}{{.Event.summary}}{{end}}{{with index .Event.metadata "url"}} ({{.}}){{end}}`

	rctx := newRenderContext(nil, event.Event{
		Type:     "github.ci_status",
		Summary:  "CI failed",
		Metadata: map[string]string{"url": "https://github.com/o/r/pull/404"},
	})
	got, err := renderField("arg", arg, rctx)
	if err != nil {
		t.Fatalf("renderField: %v", err)
	}
	if want := "[github.ci_status] CI failed (https://github.com/o/r/pull/404)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	rctx = newRenderContext(nil, event.Event{Type: "tws.instruction", Body: "do it"})
	got, err = renderField("arg", arg, rctx)
	if err != nil {
		t.Fatalf("renderField (no url): %v", err)
	}
	if got != "[tws.instruction] do it" {
		t.Errorf("got %q, want %q", got, "[tws.instruction] do it")
	}
}

func TestDeliver_ExecRenderError(t *testing.T) {
	def := config.ChannelDefinition{Type: config.ChannelTypeExec, Command: "true", Args: []string{"{{.Inputs.missing}}"}}
	err := Deliver(context.Background(), def, map[string]any{}, event.Event{})
	if err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("Deliver = %v, want a template error before exec", err)
	}
}

func TestDeliver_ExecFailure(t *testing.T) {
	def := config.ChannelDefinition{Type: config.ChannelTypeExec, Command: "false", Args: []string{"x"}}
	if err := Deliver(context.Background(), def, nil, event.Event{}); err == nil {
		t.Fatal("expected error from a non-zero exit")
	}
}

func TestDeliver_MissingInput(t *testing.T) {
	def := config.ChannelDefinition{Type: config.ChannelTypeUnixSocket, Path: "{{.Inputs.path}}", Body: "x"}
	err := Deliver(context.Background(), def, map[string]any{}, event.Event{})
	if err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("Deliver = %v, want a template error for the missing input", err)
	}
}

func TestDeliver_UnknownType(t *testing.T) {
	def := config.ChannelDefinition{Type: "webhook"}
	if err := Deliver(context.Background(), def, nil, event.Event{}); err == nil {
		t.Fatal("expected error for unknown channel type")
	}
}
