//go:build integration

package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/contracts/event"
)

// The corpus here is whatever the repository's plugins declare, discovered by
// walking the plugin root. Nothing in this file names a plugin or asserts
// anything only one of them does: core must not know which providers exist,
// so what is checked is the property every shipped channel has to have.
//
// Integration-tagged: it mounts real plugin manifests and opens real sockets.

func shippedChannels(t *testing.T) map[string]config.ChannelDefinition {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	manifests, err := filepath.Glob(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "plugins", "*", "plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Skip("no plugins in this tree")
	}
	var dirs []string
	var mounted []plugins.Mounted
	for _, path := range manifests {
		dir := filepath.Dir(path)
		manifest, err := plugins.LoadManifest(dir)
		if err != nil {
			t.Fatalf("LoadManifest(%s): %v", dir, err)
		}
		dirs = append(dirs, dir)
		// The alias is arbitrary on purpose: a shipped channel that silently
		// re-depended on one specific catalog alias would otherwise pass.
		mounted = append(mounted, plugins.Mounted{ID: "acme-mirror/" + filepath.Base(dir), Dir: dir, Manifest: manifest})
	}
	channels, err := (&config.Config{PluginDirs: dirs, Plugins: mounted}).LoadChannels()
	if err != nil {
		t.Fatalf("LoadChannels(shipped): %v", err)
	}
	if len(channels) == 0 {
		t.Skip("no shipped channels in this tree")
	}
	return channels
}

func shippedEval(t *testing.T, def config.ChannelDefinition, inputs map[string]any, ev event.Event) lang.Eval {
	runDir := t.TempDir()
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	manifests, _ := filepath.Glob(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "plugins", "*", "plugin.toml"))
	var mounted []plugins.Mounted
	for _, path := range manifests {
		dir := filepath.Dir(path)
		manifest, err := plugins.LoadManifest(dir)
		if err != nil {
			continue
		}
		mounted = append(mounted, plugins.Mounted{ID: "acme-mirror/" + filepath.Base(dir), Dir: dir, Manifest: manifest})
	}
	bins := config.MountedBins{Mounted: mounted, SourcePath: def.SourcePath}
	return deliveryEval(inputs, ev, DeliverOptions{
		Bin:      func(ref string) (string, error) { return bins.ResolveBin(ref, def.Ownership()) },
		Terminal: func(verb, _ string) (string, error) { return "terminal:" + verb, nil },
	}, runDir)
}

// standInInputs supplies one value per declared parameter that has no
// default of its own, so a channel's projections resolve without this test
// knowing what any of them mean. A parameter that declares a default keeps
// it: the default is the author's own statement of a usable value, and
// overwriting it would test a wiring no workflow produces.
func standInInputs(def config.ChannelDefinition, value string) map[string]any {
	inputs := make(map[string]any, len(def.InputSchema))
	for key, spec := range def.InputSchema {
		if spec.HasDefault {
			continue
		}
		// The stand-in names its own key, so exchanging two parameters in a
		// declaration is visible rather than invisible behind one value.
		inputs[key] = value + "-" + key
	}
	return def.ApplyInputDefaults(inputs)
}

// sampleEvents are the two shapes every shipped channel is written for: one
// carrying a body, and one carrying only a summary. They are fixed so a
// plugin's recorded invocation is reproducible, and generic so recording one
// puts no provider vocabulary in core.
func sampleEvents() []struct {
	Name  string
	Event event.Event
} {
	return []struct {
		Name  string
		Event event.Event
	}{
		{
			Name: "with-body",
			Event: event.Event{
				ID:          "01ABC",
				SessionName: "acme/widget-1",
				Type:        event.TypeUserEmit,
				Source:      event.SourcePlect,
				Direction:   event.Inbound,
				Summary:     "a summary",
				Body:        "a <tag> & an \"amp\"\nover two lines",
				Time:        time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC),
				Metadata:    map[string]string{"url": "https://example.test/x"},
			},
		},
		{
			Name: "summary-only",
			Event: event.Event{
				ID:          "01DEF",
				SessionName: "acme/widget-1",
				Type:        "example.status",
				Source:      event.SourcePlect,
				Direction:   event.Internal,
				Summary:     "only a summary",
				Time:        time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC),
			},
		},
	}
}

// recordInvocation renders what this channel hands its receiver: the argv and
// standard input of a process delivery, the bound values of a shell one (a
// shell delivery's argv is a generated wrapper path, so the bindings are the
// receiver-visible part), or the dial target and framed payload of a socket
// delivery.
func recordInvocation(t *testing.T, def config.ChannelDefinition, eval lang.Eval, inputs map[string]any, ev event.Event) (string, error) {
	root := pluginRootOf(t, def.SourcePath)
	relative := func(value string) string {
		if rest, ok := strings.CutPrefix(value, root+string(filepath.Separator)); ok {
			return "<plugin>/" + filepath.ToSlash(rest)
		}
		return value
	}
	var b strings.Builder
	fmt.Fprintf(&b, "type: %s\n", def.Type)
	switch def.Type {
	case config.ChannelTypeUnixSocket:
		path, _, err := eval.Argument(def.Path)
		if err != nil {
			return "", fmt.Errorf("path: %w", err)
		}
		body, _, err := eval.Bytes(def.Body)
		if err != nil {
			return "", fmt.Errorf("body: %w", err)
		}
		fmt.Fprintf(&b, "path: %q\nbody: %q\n", path, body)
	case config.ChannelTypeExec:
		execution, err := eval.Exec(def.Action)
		if err != nil {
			return "", err
		}
		for i, arg := range execution.Argv {
			fmt.Fprintf(&b, "argv[%d]: %q\n", i, relative(arg))
		}
		if len(execution.Stdin) > 0 {
			fmt.Fprintf(&b, "stdin: %q\n", execution.Stdin)
		}
	case config.ChannelTypeShell:
		names := make([]string, 0, len(def.Action.Bind))
		for name := range def.Action.Bind {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value, absent, err := eval.Argument(def.Action.Bind[name])
			if err != nil {
				return "", fmt.Errorf("bind.%s: %w", name, err)
			}
			if absent {
				fmt.Fprintf(&b, "bind.%s: <unset>\n", name)
				continue
			}
			fmt.Fprintf(&b, "bind.%s: %q\n", name, relative(value))
		}
	}
	timeout, err := ResolveTimeout(def, inputs)
	if err != nil {
		return "", fmt.Errorf("timeout: %w", err)
	}
	fmt.Fprintf(&b, "timeout: %s\n", timeout)
	if def.Type == config.ChannelTypeShell {
		observed, err := observeShellDelivery(t, def, inputs, ev)
		if err != nil {
			return "", err
		}
		for i, line := range observed {
			fmt.Fprintf(&b, "observed[%d]: %q\n", i, line)
		}
	}
	return b.String(), nil
}

// observeShellDelivery runs a shell channel for real, with every capability
// it consumes bound to a stub that records what it was asked to do. A shell
// script is the plugin's own logic, so what its receiver observes is the
// sequence of capability calls — reordering the script, dropping a step, or
// merging two of them changes this and nothing else would.
func observeShellDelivery(t *testing.T, def config.ChannelDefinition, inputs map[string]any, ev event.Event) ([]string, error) {
	log := filepath.Join(t.TempDir(), "observed.log")
	terminal := func(verb, _ string) (string, error) {
		// Every verb records itself, the readiness capture included: dropping
		// that step from a script is a change to what the receiver observes,
		// so it has to be visible here. A capture additionally answers on
		// stdout with an already-submitted input box, which is what lets a
		// readiness loop finish instead of exhausting its backoff.
		// One call is one record, delimited rather than newline-separated:
		// an operand may itself span lines, and a step has to stay countable.
		stub := `printf '` + verb + `:%s\036' "${1-}" >> ` + log
		if verb == "capture" {
			stub += `; printf '\n'`
		}
		return stub, nil
	}
	opts := DeliverOptions{Terminal: terminal}
	if err := DeliverWithOptions(context.Background(), def, inputs, ev, opts); err != nil {
		return nil, fmt.Errorf("shell delivery: %w", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		return nil, fmt.Errorf("read observed log: %w", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x1e"), "\x1e"), nil
}

// TestShippedChannels_InvocationsMatchTheirPluginsRecord is the link between a
// shipped channel declaration and what its receiver gets. Each plugin that
// ships channels records, in its own testdata, the invocation those channels
// must produce for the fixed sample events above; this regenerates that record
// and compares. Swapping two argv values, renaming a payload key, or dropping
// a binding changes the record, so the diff a reviewer sees is the receiver
// contract changing.
//
// Set PLECT_UPDATE_CHANNEL_RECORDS=1 to rewrite the records after an
// intended contract change.
func TestShippedChannels_InvocationsMatchTheirPluginsRecord(t *testing.T) {
	// Keyed by the declaration's own id rather than its catalog address: the
	// address carries the alias this test mounts under, and a record that
	// pinned an alias would be asserting the test's own setup.
	byPlugin := map[string]map[string]config.ChannelDefinition{}
	for _, def := range shippedChannels(t) {
		root := pluginRootOf(t, def.SourcePath)
		if byPlugin[root] == nil {
			byPlugin[root] = map[string]config.ChannelDefinition{}
		}
		byPlugin[root][def.ID] = def
	}
	for root, channels := range byPlugin {
		t.Run(filepath.Base(root), func(t *testing.T) {
			var b strings.Builder
			ids := make([]string, 0, len(channels))
			for id := range channels {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				def := channels[id]
				inputs := standInInputs(def, "stand-in")
				for _, sample := range sampleEvents() {
					fmt.Fprintf(&b, "== %s / %s\n", id, sample.Name)
					record, err := recordInvocation(t, def, shippedEval(t, def, inputs, sample.Event), inputs, sample.Event)
					if err != nil {
						t.Fatalf("%s / %s: %v", id, sample.Name, err)
					}
					b.WriteString(record)
					b.WriteString("\n")
				}
			}
			record := filepath.Join(root, "testdata", "channel-invocations.txt")
			if os.Getenv("PLECT_UPDATE_CHANNEL_RECORDS") == "1" {
				if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("rewrote %s", record)
				return
			}
			want, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("a plugin shipping channels records the invocations they must produce: %v", err)
			}
			if produced := strings.TrimRight(b.String(), "\n") + "\n"; string(want) != produced {
				t.Errorf("invocations changed.\n--- recorded\n%s\n--- produced\n%s", want, produced)
			}
		})
	}
}

func pluginRootOf(t *testing.T, sourcePath string) string {
	t.Helper()
	dir := filepath.Dir(sourcePath)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("no plugin.toml above %s", sourcePath)
	return ""
}

// A unix_socket channel's receiver decodes one framed envelope per delivery,
// so this drives a real delivery into a stand-in listener and asserts the
// envelope carries the event.
func TestShippedChannels_UnixSocketDeliversOneFramedEnvelope(t *testing.T) {
	ev := event.Event{
		ID:          "01ABC",
		SessionName: "acme/widget-1",
		Type:        event.TypeUserEmit,
		Summary:     "s",
		Body:        `a <tag> & an "amp"`,
		Metadata:    map[string]string{"url": "https://example.test/x"},
	}
	delivered := 0
	for id, def := range shippedChannels(t) {
		if def.Type != config.ChannelTypeUnixSocket {
			continue
		}
		delivered++
		t.Run(id, func(t *testing.T) {
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
				if data, err := readFramed(conn); err == nil {
					received <- data
				}
			}()

			// Every declared parameter gets the socket path verbatim, so
			// whichever one this channel's `path` projects resolves to the
			// listener without this test knowing its name.
			inputs := make(map[string]any, len(def.InputSchema))
			for key := range def.InputSchema {
				inputs[key] = sock
			}
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
				Type    string                `json:"type"`
				Payload struct{ Text string } `json:"payload"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatalf("envelope is not JSON: %v (%s)", err, data)
			}
			if envelope.Type != string(protocol.MsgMessage) {
				t.Errorf("envelope type = %q, want %q", envelope.Type, protocol.MsgMessage)
			}
			var carried map[string]any
			if err := json.Unmarshal([]byte(envelope.Payload.Text), &carried); err != nil {
				t.Fatalf("payload is not the event as JSON: %v (%s)", err, envelope.Payload.Text)
			}
			if carried["id"] != ev.ID || carried["body"] != ev.Body {
				t.Errorf("carried = %v, want the delivered event", carried)
			}
			md, ok := carried["metadata"].(map[string]any)
			if !ok || md["url"] != "https://example.test/x" {
				t.Errorf("carried metadata = %v, want the event's metadata", carried["metadata"])
			}
		})
	}
	if delivered == 0 {
		t.Skip("no shipped unix_socket channel in this tree")
	}
}

// A shell channel splitting one delivery into a sequence of terminal
// commands is the shape an interactive runtime needs — text, a pause, then a
// separate key — and it only works if each bound capability reaches the
// script as data and the script's own ordering is preserved. The channel here
// is synthetic: what is under test is the engine, not any one runtime's
// contract.
func TestDeliver_ShellChannelSequencesTerminalCommands(t *testing.T) {
	log := filepath.Join(t.TempDir(), "terminal.log")
	def := channelDef(t, `
[c]
kind   = "channel"
type   = "shell"
script = '''
sh -c "$send_text" channel-send-text "$message"
sleep 0.2
sh -c "$send_keys" channel-send-keys Enter
'''

[c.bind]
send_text = { terminal = "send_text" }
send_keys = { terminal = "send_keys" }
message   = { expr = "'[' + event.type + '] ' + (event.body != '' ? event.body : event.summary)" }
`)
	terminal := func(verb, _ string) (string, error) {
		return `printf '` + verb + `:%s\n' "$1" >> ` + log, nil
	}
	ev := event.Event{Type: event.TypeUserEmit, Summary: "s", Body: "run the tests"}
	err := DeliverWithOptions(context.Background(), def, nil, ev, DeliverOptions{Terminal: terminal})
	if err != nil {
		t.Fatalf("DeliverWithOptions: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read terminal log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"send_text:[" + ev.Type + "] run the tests", "send_keys:Enter"}
	if len(lines) != len(want) {
		t.Fatalf("terminal saw %d commands, want %d: %q", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
