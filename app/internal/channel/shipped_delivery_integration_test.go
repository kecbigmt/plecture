//go:build integration

package channel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/contracts/event"
)

// These tests assert what each shipped channel's *receiver* observes — a
// framed socket message, a queued file, an HTTP request body, a sequence of
// terminal commands — rather than what its fields render to. That is the
// contract the plugin's counterpart depends on, and it is the property that
// has to survive a change of declaration form.
//
// They are integration-tagged because they run real processes (bash, jq,
// curl) and one of them waits out the Codex TUI's paste-burst window.

func shippedChannels(t *testing.T) map[string]config.ChannelDefinition {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	var dirs []string
	var mounted []plugins.Mounted
	for _, name := range []string{"claude", "codex", "slack"} {
		dir := filepath.Join(repoRoot, "plugins", name)
		manifest, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", name, err)
		}
		dirs = append(dirs, dir)
		// The alias is deliberately not "official": a shipped channel that
		// silently re-depended on one specific catalog alias would otherwise
		// pass unnoticed.
		mounted = append(mounted, plugins.Mounted{ID: "acme-mirror/" + name, Dir: dir, Manifest: manifest})
	}
	channels, err := (&config.Config{PluginDirs: dirs, Plugins: mounted}).LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped catalog): %v", err)
	}
	return channels
}

// shippedBin resolves a shipped channel's `bin` the way the dispatcher does.
func shippedBin(def config.ChannelDefinition) BinResolver {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	var mounted []plugins.Mounted
	for _, name := range []string{"claude", "codex", "slack"} {
		dir := filepath.Join(repoRoot, "plugins", name)
		manifest, err := plugins.LoadManifest(dir)
		if err != nil {
			continue
		}
		mounted = append(mounted, plugins.Mounted{ID: "acme-mirror/" + name, Dir: dir, Manifest: manifest})
	}
	bins := config.MountedBins{Mounted: mounted, SourcePath: def.SourcePath}
	return func(ref string) (string, error) { return bins.ResolveBin(ref, def.Ownership()) }
}

func shippedChannel(t *testing.T, id string) config.ChannelDefinition {
	t.Helper()
	def, ok := shippedChannels(t)[id]
	if !ok {
		t.Fatalf("shipped channel %q not found", id)
	}
	return def
}

// The claude channel's receiver is channel-server, which decodes one framed
// envelope per delivery. The assertion is JSON equivalence rather than byte
// equality: what the server relies on is the decoded event, not one
// serializer's escaping choices.
func TestShippedDelivery_ClaudeWritesTheEventAsAFramedMessage(t *testing.T) {
	def := shippedChannel(t, "claude")
	sock := filepath.Join(t.TempDir(), "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	received := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		data, err := readFramed(conn)
		if err != nil {
			return
		}
		received <- data
	}()

	ev := event.Event{
		ID:          "01ABC",
		SessionName: "o/r-1",
		Type:        event.TypeUserEmit,
		Summary:     "s",
		Body:        `a <tag> & an "amp"`,
		Time:        time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC),
		Metadata:    map[string]string{"url": "https://example.test/x"},
	}
	inputs := def.ApplyInputDefaults(map[string]any{"path": sock})
	if err := Deliver(context.Background(), def, inputs, ev); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	var data []byte
	select {
	case data = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no framed message arrived")
	}

	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("envelope is not JSON: %v (%s)", err, data)
	}
	if envelope.Type != string(protocol.MsgMessage) {
		t.Errorf("envelope type = %q, want %q", envelope.Type, protocol.MsgMessage)
	}
	var carried map[string]any
	if err := json.Unmarshal([]byte(envelope.Payload.Text), &carried); err != nil {
		t.Fatalf("payload text is not the event as JSON: %v (%s)", err, envelope.Payload.Text)
	}
	for key, want := range map[string]any{
		"id":           ev.ID,
		"session_name": ev.SessionName,
		"type":         ev.Type,
		"summary":      ev.Summary,
		"body":         ev.Body,
	} {
		if carried[key] != want {
			t.Errorf("carried[%q] = %v, want %v", key, carried[key], want)
		}
	}
	md, ok := carried["metadata"].(map[string]any)
	if !ok || md["url"] != "https://example.test/x" {
		t.Errorf("carried metadata = %v, want the event's url", carried["metadata"])
	}
}

// The codex_exec channel's receiver is codex-exec-worker, which reads the
// {type, text} files the enqueue script leaves in the queue directory. The
// message_envelope parameter is expanded by that script, so the queued text
// is where its effect is observable.
func TestShippedDelivery_CodexExecQueuesAnExpandedEnvelope(t *testing.T) {
	def := shippedChannel(t, "codex_exec")
	queueDir := filepath.Join(t.TempDir(), "queue")
	ev := event.Event{
		Type:     event.TypeUserEmit,
		Summary:  "fallback summary",
		Body:     "do the thing",
		Metadata: map[string]string{"url": "https://example.test/pr/1"},
	}
	inputs := def.ApplyInputDefaults(map[string]any{"queue_dir": queueDir})
	if err := DeliverWithOptions(context.Background(), def, inputs, ev, DeliverOptions{Bin: shippedBin(def)}); err != nil {
		t.Fatalf("DeliverWithOptions: %v", err)
	}

	entries, err := os.ReadDir(queueDir)
	if err != nil {
		t.Fatalf("read queue dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("queued %d files, want 1", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(queueDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var queued struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &queued); err != nil {
		t.Fatalf("queued file is not JSON: %v (%s)", err, raw)
	}
	if queued.Type != ev.Type {
		t.Errorf("queued type = %q, want %q", queued.Type, ev.Type)
	}
	// The default envelope is "[{type}] {body_or_summary}{url_suffix}".
	want := "[" + ev.Type + "] do the thing (https://example.test/pr/1)"
	if queued.Text != want {
		t.Errorf("queued text = %q, want %q", queued.Text, want)
	}
}

// An event with no body falls back to its summary, which is the branch a
// status event takes.
func TestShippedDelivery_CodexExecFallsBackToTheSummary(t *testing.T) {
	def := shippedChannel(t, "codex_exec")
	queueDir := filepath.Join(t.TempDir(), "queue")
	ev := event.Event{Type: "github.ci_status", Summary: "CI failed"}
	inputs := def.ApplyInputDefaults(map[string]any{"queue_dir": queueDir})
	if err := DeliverWithOptions(context.Background(), def, inputs, ev, DeliverOptions{Bin: shippedBin(def)}); err != nil {
		t.Fatalf("DeliverWithOptions: %v", err)
	}
	entries, err := os.ReadDir(queueDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	raw, _ := os.ReadFile(filepath.Join(queueDir, entries[0].Name()))
	var queued struct{ Text string }
	if err := json.Unmarshal(raw, &queued); err != nil {
		t.Fatal(err)
	}
	if want := "[github.ci_status] CI failed"; queued.Text != want {
		t.Errorf("queued text = %q, want %q", queued.Text, want)
	}
}

// The slack channel's receiver is this plugin's slack-adapter HTTP API. What
// it depends on is the posted JSON object, so the assertion decodes the
// request body rather than comparing the argv that produced it.
func TestShippedDelivery_SlackPostsTheEventAsJSON(t *testing.T) {
	def := shippedChannel(t, "slack")

	type posted struct {
		ThreadTS  string `json:"thread_ts"`
		ChannelID string `json:"channel_id"`
		Text      string `json:"text"`
	}
	got := make(chan posted, 2)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var p posted
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got <- p
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		ev   event.Event
		want string
	}{
		{
			name: "a body is posted verbatim",
			ev:   event.Event{Type: event.TypeUserEmit, Summary: "s", Body: "kick \"now\"\nplease"},
			want: "kick \"now\"\nplease",
		},
		{
			name: "an empty body falls back to the summary",
			ev:   event.Event{Type: "github.ci_status", Summary: "CI failed: test"},
			want: "CI failed: test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := def.ApplyInputDefaults(map[string]any{
				"base_url":   srv.URL,
				"channel_id": "C0",
				"thread_ts":  "12.34",
			})
			if err := DeliverWithOptions(context.Background(), def, inputs, tc.ev, DeliverOptions{Bin: shippedBin(def)}); err != nil {
				t.Fatalf("DeliverWithOptions: %v", err)
			}
			select {
			case p := <-got:
				if p.Text != tc.want {
					t.Errorf("text = %q, want %q", p.Text, tc.want)
				}
				if p.ThreadTS != "12.34" || p.ChannelID != "C0" {
					t.Errorf("thread_ts/channel_id = %q/%q", p.ThreadTS, p.ChannelID)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("no request arrived")
			}
			if gotPath != "/messages" {
				t.Errorf("path = %q, want /messages", gotPath)
			}
		})
	}
}

// The terminal_submit channel's receiver is a terminal: what it depends on is
// the *sequence* of terminal commands — the text, then a separate Enter, with
// the readiness re-check in between — because the Codex TUI treats an Enter
// arriving inside the same burst as a newline rather than a submit.
func TestShippedDelivery_TerminalSubmitSplitsTextFromEnter(t *testing.T) {
	def := shippedChannel(t, "terminal_submit")
	log := filepath.Join(t.TempDir(), "terminal.log")

	// Each verb resolves to a command the script runs as `sh -c <cmd> <argv0>
	// <operand>`, so "$1" is the operand the script passes for that verb.
	// capture reports an empty prompt line, which is how a submitted input box
	// looks — the script then stops after the first Enter.
	terminal := func(verb string) (string, error) {
		switch verb {
		case "send_text":
			return `printf 'text:%s\n' "$1" >> ` + log, nil
		case "send_keys":
			return `printf 'keys:%s\n' "$1" >> ` + log, nil
		case "capture":
			return `printf '\n'`, nil
		}
		return "", nil
	}
	ev := event.Event{
		Type:     event.TypeUserEmit,
		Summary:  "s",
		Body:     "run the tests",
		Metadata: map[string]string{"url": "https://example.test/x"},
	}
	inputs := def.ApplyInputDefaults(map[string]any{})
	if err := DeliverWithOptions(context.Background(), def, inputs, ev, DeliverOptions{Terminal: terminal}); err != nil {
		t.Fatalf("DeliverWithOptions: %v", err)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read terminal log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("terminal saw %d commands, want the text and one Enter: %q", len(lines), lines)
	}
	wantText := "text:[" + ev.Type + "] run the tests (https://example.test/x)"
	if lines[0] != wantText {
		t.Errorf("first command = %q, want %q", lines[0], wantText)
	}
	if lines[1] != "keys:Enter" {
		t.Errorf("second command = %q, want an Enter of its own", lines[1])
	}
}
