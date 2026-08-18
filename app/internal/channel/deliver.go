// Package channel renders and executes a workflow [[event.channel]]'s delivery
// of one session event to an external runtime. It is the engine the session
// dispatcher drives: the dispatcher resolves which channels match an event and
// supplies the channel inputs (workflow node outputs); this package renders the
// channel definition's primitive fields against {.Event, .Inputs} and runs the
// built-in primitive (unix_socket or exec).
package channel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	protocol "github.com/kecbigmt/plecture/contracts/channel-protocol"
	"github.com/kecbigmt/plecture/contracts/event"
)

// renderContext is what a channel template sees: the event as a map keyed by the
// envelope's json field names (so {{.Event.type}}/{{.Event.body}} resolve) and
// the resolved channel inputs.
type renderContext struct {
	Event  map[string]any
	Inputs map[string]any
	// Terminal backs {{terminal "..."}} in an args/path/body template. Nil
	// when the caller passed no DeliverOptions.Terminal (the session's plan
	// declares no [terminal]-owning task, or the caller has none in scope)
	// — a channel that never references {{terminal ...}} is unaffected.
	Terminal TerminalResolver
}

// TerminalResolver resolves one [terminal] verb (attach/capture/send_text/
// send_keys) to its already-rendered command string, backing {{terminal
// "..."}} in channel argument templates. Defined here as a plain function
// type — not imported from internal/task — so this package stays free of a
// dependency on internal/task, the same reason Executor is narrowed to
// argv-only instead of reusing task.Executor; dispatch (which already
// depends on both) builds the closure.
type TerminalResolver func(verb string) (string, error)

var templateFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
}

// eventMap exposes the event with every standard envelope field present (empty
// when unset) so {{.Event.body}} on an empty-body event renders empty under
// missingkey=error instead of failing. Building it explicitly (not via a json
// round-trip) also keeps any future numeric field from being coerced to float64.
func eventMap(ev event.Event) map[string]any {
	md := make(map[string]any, len(ev.Metadata))
	for k, v := range ev.Metadata {
		md[k] = v
	}
	return map[string]any{
		"id":           ev.ID,
		"session_name": ev.SessionName,
		"time":         ev.Time.Format(time.RFC3339Nano),
		"type":         ev.Type,
		"source":       ev.Source,
		"direction":    string(ev.Direction),
		"summary":      ev.Summary,
		"body":         ev.Body,
		"metadata":     md,
	}
}

func newRenderContext(inputs map[string]any, ev event.Event, terminal TerminalResolver) renderContext {
	if inputs == nil {
		inputs = map[string]any{}
	}
	return renderContext{Event: eventMap(ev), Inputs: inputs, Terminal: terminal}
}

// renderField renders one primitive field. missingkey=error so a typo'd field or
// an unwired .Inputs key fails delivery rather than sending an empty value;
// standard .Event fields are always present (see eventMap), so an optional field
// that is merely empty renders empty, not an error.
func renderField(name, tmplStr string, rctx renderContext) (string, error) {
	// terminal is built per render call (not part of the static
	// templateFuncs map) because it resolves against this render's own
	// rctx.Terminal — the session in effect for this delivery, not a global.
	dynamicFuncs := template.FuncMap{
		"terminal": func(verb string) (string, error) {
			if rctx.Terminal == nil {
				return "", fmt.Errorf("{{terminal %q}}: no task in this session's plan declares [terminal]", verb)
			}
			return rctx.Terminal(verb)
		},
	}
	t, err := template.New(name).Option("missingkey=error").Funcs(templateFuncs).Funcs(dynamicFuncs).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("channel %s template: %w", name, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, rctx); err != nil {
		return "", fmt.Errorf("channel %s template: %w", name, err)
	}
	return b.String(), nil
}

// DeliverOptions carries optional per-delivery overrides beyond the channel
// definition and event itself. The zero value is what Deliver uses: no
// {{terminal "..."}} binding.
type DeliverOptions struct {
	// Terminal resolves {{terminal "..."}} in this delivery's templates; nil
	// means a channel that references it gets a clear "no binding" error
	// rather than resolving to an empty command.
	Terminal TerminalResolver
}

// Deliver renders a channel definition against the event and resolved inputs,
// then performs ONE delivery attempt on the host. The caller resolves inputs
// (node outputs) and owns retry/timeout (see DeliverWithRetry). Equivalent to
// DeliverWithOptions with the zero DeliverOptions.
func Deliver(ctx context.Context, def config.ChannelDefinition, inputs map[string]any, ev event.Event) error {
	return deliver(ctx, def, inputs, ev, DeliverOptions{})
}

// DeliverWithOptions is Deliver with the full set of per-delivery overrides.
func DeliverWithOptions(ctx context.Context, def config.ChannelDefinition, inputs map[string]any, ev event.Event, opts DeliverOptions) error {
	return deliver(ctx, def, inputs, ev, opts)
}

func deliver(ctx context.Context, def config.ChannelDefinition, inputs map[string]any, ev event.Event, opts DeliverOptions) error {
	rctx := newRenderContext(inputs, ev, opts.Terminal)
	switch def.Type {
	case config.ChannelTypeUnixSocket:
		return deliverUnixSocket(ctx, def, rctx)
	case config.ChannelTypeExec:
		return deliverExec(ctx, def, rctx)
	default:
		return fmt.Errorf("unknown channel type %q", def.Type)
	}
}

func deliverUnixSocket(ctx context.Context, def config.ChannelDefinition, rctx renderContext) error {
	path, err := renderField("path", def.Path, rctx)
	if err != nil {
		return err
	}
	body, err := renderField("body", def.Body, rctx)
	if err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("dial %s: %w", path, err)
	}
	defer conn.Close()
	// Bound the write even when the caller passes a deadline-less ctx, so a peer
	// that holds the connection open without reading can't block Deliver forever.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(fallbackWriteDeadline)
	}
	_ = conn.SetWriteDeadline(deadline)
	// Source stays empty: a non-empty Source lets channel-server treat a
	// "y/n <id>" body as a human permission verdict, so a content event must not
	// set it.
	data, err := protocol.NewEnvelope(protocol.MsgMessage, protocol.MessagePayload{Text: body})
	if err != nil {
		return err
	}
	return writeFramed(conn, data)
}

// maxFrame matches channel-server's readMessage cap. Rejecting here turns an
// oversized payload into a clean error instead of a write that succeeds locally
// but is dropped (and the connection closed) by the server.
const maxFrame = 1 << 20

const fallbackWriteDeadline = 30 * time.Second

func writeFramed(conn net.Conn, data []byte) error {
	if len(data) > maxFrame {
		return fmt.Errorf("channel message %d bytes exceeds %d limit", len(data), maxFrame)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

func deliverExec(ctx context.Context, def config.ChannelDefinition, rctx renderContext) error {
	args := make([]string, len(def.Args))
	for i, a := range def.Args {
		v, err := renderField(fmt.Sprintf("args[%d]", i), a, rctx)
		if err != nil {
			return err
		}
		args[i] = v
	}
	// Command is verbatim, never templated, so event/input data can never choose
	// the executable — only the argv values are rendered.
	cmd := exec.CommandContext(ctx, def.Command, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("exec %s: %w: %s", def.Command, err, msg)
		}
		return fmt.Errorf("exec %s: %w", def.Command, err)
	}
	return nil
}
