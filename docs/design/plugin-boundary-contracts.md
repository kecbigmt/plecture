# Plugin boundary contracts

This design is governed by
[`../adr/2026-08-17-plugin-boundary-contracts.md`](../adr/2026-08-17-plugin-boundary-contracts.md).

## Design Core

Plugin boundaries are package boundaries, not runtime-isolation boundaries. A
plugin may collaborate with another plugin only through core-owned composition,
provider-neutral contracts, provider-neutral event-bus types, or opaque
plugin-owned event payloads.

The load-bearing rules are:

- shipped plugin configuration references only executables declared by the same
  plugin;
- user-owned workflow and task overlays compose plugin definitions by id;
- reusable terminal integration crosses plugin boundaries through an opaque
  `interactive_endpoint` binding plus raw terminal verbs, not through a
  multiplexer-specific handle;
- agent-runtime plugins own submit and readiness composition for their own
  interactive TUIs;
- structured delivery is the supported path when an agent runtime provides it;
- human conversation and agent replies rendezvous on provider-neutral
  `conversation.*` events;
- concrete provider guards, watchers, chat adapters, agent CLIs, and
  multiplexers stay in provider plugins.

## Plugin Ownership

The reusable session runtime surface is split into independently selectable
plugins:

| Plugin | Owns | Core contracts used | Excludes |
|---|---|---|---|
| `session/tmux` | tmux-backed interactive endpoint task and terminal operation declarations | interactive endpoint, terminal operations, task lifecycle | agent CLIs, agent-TUI submit/readiness logic, chat delivery, VCS guards |
| `session/claude` | Claude Code launch tasks, initial-prompt submit/readiness logic, structured Claude Code delivery, channel-server service, Claude activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Codex, chat-service adapters, VCS guards |
| `session/codex` | Codex TUI and `codex exec` launch tasks, initial-prompt and terminal-submit readiness logic, queue worker, enqueue channel, Codex activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Claude, chat-service adapters, VCS guards |
| `slack-delivery` | Slack adapter service, Slack thread binding, Slack event ingress and egress | conversation events, plugin services, channel delivery | agent runtimes, channel-server sockets, VCS guards |
| `github` | GitHub resource observation, workspace acquisition, watcher service, GitHub CLI write guard | resource definitions, workspace providers, subscriptions, plugin services | session runtime tasks, chat-service adapters |

A plugin with only workflow, task, channel, template, or other configuration
resources is still a plugin. A shared review workflow may be distributed as a
configuration-only plugin, or it may remain user-owned workflow and template
configuration.

## Terminal Operation Surface

A task that owns an interactive terminal endpoint declares:

- `setup`, returning an `interactive_endpoint` output;
- top-level `cleanup` and `healthcheck` task keys;
- a `[terminal]` table with `attach`, `capture`, `send_text`, and `send_keys`.

The `[terminal]` table is all-or-nothing: declaring any member requires all four
members, and a partial table is a load error. Each member is a
Go-template-rendered shell command string. Single-line and multi-line TOML
strings are valid; arrays are invalid.

`attach` and `capture` receive no terminal operand. `send_text` and `send_keys`
receive the literal text or key token as their first shell positional
parameter.

Terminal commands are raw verbs. `send_text` sends literal text, `send_keys`
sends key-combo input, and `capture` returns terminal text for the consumer to
interpret. Agent-runtime plugins compose those verbs into submit/readiness
behavior.

### tmux Provider

```toml
# The task also declares setup, cleanup, healthcheck, and an
# interactive_endpoint output.

[terminal]
attach = "..."
capture = "..."
send_text = "tmux send-keys -t {{.Self.interactive_endpoint}} -- \"$1\""
send_keys = "..."
```

The `interactive_endpoint` value is opaque to consumers. The tmux task uses a
session name; a different multiplexer may use a pane id, socket path token, or
other implementation-owned binding.

### Codex Terminal Submit

`session/codex` owns Codex TUI submission because the burst split, readiness
predicate, retry policy, and fail-loud behavior describe the Codex TUI
contract.

```toml
type = "exec"
command = "bash"
args = [
  "-c",
  '''
send_text_cmd="$1"
send_keys_cmd="$2"
capture_cmd="$3"
message="$4"

# session/codex owns the burst split, readiness predicate, retry policy, and
# fail-loud behavior around these raw terminal calls.
sh -c "$send_text_cmd" terminal-send-text "$message"
sh -c "$send_keys_cmd" terminal-send-keys Enter
sh -c "$capture_cmd" terminal-capture
''',
  "terminal_submit",
  "{{terminal \"send_text\"}}",
  "{{terminal \"send_keys\"}}",
  "{{terminal \"capture\"}}",
  '[{{.Event.type}}] {{if .Event.body}}{{.Event.body}}{{else}}{{.Event.summary}}{{end}}{{with index .Event.metadata "url"}} ({{.}}){{end}}',
]
timeout = "45s"
```

The channel command is static. Rendered `{{terminal "..."}}` values and event
data ride in `args`, so event data can choose operands but not the executable.
The readiness predicate and retry details live in the shipped `session/codex`
config.

Claude Code delivery uses channel-server as its structured delivery path. A
no-channel-server interactive Claude configuration is outside the supported
surface. Raw terminal submit is the fallback for interactive TUIs without
structured transport.

### Herdr Provider

```toml
# The task returns an opaque Herdr pane id as interactive_endpoint.

[terminal]
attach = "..."
capture = "..."
send_text = "herdr pane.send_text {{.Self.interactive_endpoint}} \"$1\""
send_keys = "..."
```

Herdr pane ids such as `w1:p1` fit the same opaque binding. Herdr's
`HERDR_SOCKET_PATH` and `HERDR_PANE_ID` environment values are operation
context, not consumer-visible endpoint shape. Herdr capabilities such as
semantic agent status, `agent.wait`, and `events.subscribe` are optional
extensions outside the required terminal surface.

## Conversation Events

Core owns these conversation event types:

| Type | Direction | Meaning |
|---|---|---|
| `conversation.message` | inbound | A human-authored or operator-authored message addressed to the session |
| `conversation.reply` | outbound | Agent-authored text for the active conversation |
| `conversation.permission_request` | outbound | A runtime asks the human conversation for a decision |
| `conversation.permission_reply` | inbound | A human decision for a permission request |

A Slack-to-agent flow uses the bus as the rendezvous:

1. `slack-delivery` receives a Slack message and publishes
   `conversation.message` with opaque Slack correlation metadata.
2. `session/claude` consumes `conversation.message` through its structured
   channel-server delivery path, or `session/codex` consumes the same event
   through its terminal-submit channel.
3. The agent runtime publishes `conversation.reply` or
   `conversation.permission_request`.
4. `slack-delivery` maps those events back to the bound Slack thread.
5. A Slack decision publishes `conversation.permission_reply` for the waiting
   runtime.

The channel-server socket protocol is an implementation protocol for a
structured agent runtime. Chat-delivery plugins do not connect to
channel-server sockets and do not import channel-server client packages. The
socket protocol belongs with the structured agent runtime implementation that
uses it, not in a shared `contracts/` package without another concrete
provider-neutral consumer.

## Review Workflow Composition

The team review workflow is configuration-level composition:

1. a resource definition observes the review request;
2. a workspace provider acquires the work surface;
3. a workflow starts a selected multiplexer endpoint and a selected agent
   runtime;
4. a delivery plugin binds the team conversation and maps messages through the
   event bus;
5. `done_when`, judge, and terminal events record verification and handoff.

No review-workflow executable plugin is required.

## GitHub CLI Guard

The GitHub CLI write guard belongs to the `github` plugin. Workflows that need
the guard compose the GitHub plugin's guard task or environment output as an
upstream runtime input. Agent plugins accept only generic environment or path
inputs; they do not carry a GitHub-specific guard switch.
