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
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
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
		Terminal: func(verb string) (string, error) { return "true", nil },
	})
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
		inputs[key] = value
	}
	return def.ApplyInputDefaults(inputs)
}

func declaredValues(def config.ChannelDefinition) map[string]*lang.Value {
	values := map[string]*lang.Value{}
	if def.Path != nil {
		values["path"] = def.Path
	}
	if def.Body != nil {
		values["body"] = def.Body
	}
	if def.Action != nil {
		for i, arg := range def.Action.Args {
			values[fmt.Sprintf("args[%d]", i)] = arg
		}
		if def.Action.Stdin != nil {
			values["stdin"] = def.Action.Stdin
		}
		for name, bound := range def.Action.Bind {
			values["bind."+name] = bound
		}
	}
	return values
}

// Every value a shipped channel declares has to resolve against an event and
// its own declared parameters — a broken projection, an expression naming a
// root the surface does not offer, or an unresolvable executable would
// otherwise only surface on the first event that channel is asked to deliver.
func TestShippedChannels_EveryDeclaredValueResolves(t *testing.T) {
	ev := event.Event{
		ID:          "01ABC",
		SessionName: "acme/widget-1",
		Type:        event.TypeUserEmit,
		Summary:     "a summary",
		Body:        `a <tag> & an "amp"`,
		Time:        time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC),
		Metadata:    map[string]string{"url": "https://example.test/x"},
	}
	for id, def := range shippedChannels(t) {
		t.Run(id, func(t *testing.T) {
			inputs := standInInputs(def, "stand-in")
			eval := shippedEval(t, def, inputs, ev)
			for where, value := range declaredValues(def) {
				resolved, absent, err := eval.Bytes(value)
				if err != nil {
					t.Errorf("%s: %v", where, err)
					continue
				}
				if absent {
					continue
				}
				// A serializing operand has to produce a parseable document:
				// that is the whole reason it is an operand rather than a
				// string a value could malform.
				if value.Form == lang.FormJSON {
					var parsed any
					if err := json.Unmarshal(resolved, &parsed); err != nil {
						t.Errorf("%s: serialized operand is not JSON: %v (%s)", where, err, resolved)
					}
				}
			}
			if _, err := ResolveTimeout(def, inputs); err != nil {
				t.Errorf("timeout: %v", err)
			}
		})
	}
}

// An event with no body must resolve as readily as one with a body: a
// fallback to the summary is the shape every shipped channel is written for,
// and an unset optional field is empty rather than absent.
func TestShippedChannels_ResolveWithoutABody(t *testing.T) {
	ev := event.Event{Type: "example.status", Summary: "only a summary"}
	for id, def := range shippedChannels(t) {
		t.Run(id, func(t *testing.T) {
			eval := shippedEval(t, def, standInInputs(def, "stand-in"), ev)
			for where, value := range declaredValues(def) {
				if _, _, err := eval.Bytes(value); err != nil {
					t.Errorf("%s: %v", where, err)
				}
			}
		})
	}
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

			// Every declared parameter gets the socket path, so whichever one
			// this channel's `path` projects resolves to the listener without
			// this test knowing its name.
			if err := Deliver(context.Background(), def, standInInputs(def, sock), ev); err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			var data []byte
			select {
			case data = <-received:
			case <-time.After(5 * time.Second):
				t.Fatal("no framed message arrived")
			}
			var envelope struct {
				Payload struct{ Text string } `json:"payload"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatalf("envelope is not JSON: %v (%s)", err, data)
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
	terminal := func(verb string) (string, error) {
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
