# Plugin boundary contracts

This design is governed by
[`../adr/2026-08-17-plugin-boundary-contracts.md`](../adr/2026-08-17-plugin-boundary-contracts.md),
with the Terminal Operation Surface's process-starting boundary decided by
[`../adr/2026-08-24-effect-setup-process-substep.md`](../adr/2026-08-24-effect-setup-process-substep.md).

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

The reusable runtime surface is split into independently selectable
plugins:

| Plugin | Owns | Core contracts used | Excludes |
|---|---|---|---|
| `tmux` | tmux-backed interactive endpoint task and terminal operation declarations | interactive endpoint, terminal operations, task lifecycle | agent CLIs, agent-TUI submit/readiness logic, chat delivery, VCS guards |
| `claude` | Claude Code launch tasks, initial-prompt submit/readiness logic, structured Claude Code delivery, channel-server service, Claude activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Codex, chat-service adapters, VCS guards |
| `codex` | Codex TUI and `codex exec` launch tasks, initial-prompt and terminal-submit readiness logic, queue worker, enqueue channel, Codex activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Claude, chat-service adapters, VCS guards |
| `slack` | Slack adapter service, Slack thread binding, Slack event ingress and egress | conversation events, plugin services, channel delivery | agent runtimes, channel-server sockets, VCS guards |
| `github` | GitHub resource observation, workspace acquisition, watcher service, GitHub CLI write guard | resource definitions, workspace providers, subscriptions, plugin services | session runtime tasks, chat-service adapters |

A plugin with only workflow, task, channel, or other configuration
resources is still a plugin. A shared review workflow may be distributed as a
configuration-only plugin, or it may remain user-owned workflow
configuration.

## Terminal Operation Surface

A task that owns an interactive terminal endpoint declares:

- `setup`, returning an `interactive_endpoint` output;
- top-level `cleanup` and a `[health]` table (see
  [`health-declaration.md`](health-declaration.md));
- a `[terminal]` table with `attach`, `capture`, `send_text`, and `send_keys`.

Availability is per verb: an effect declares the verbs it can honor, and a
value consuming a verb no effect in the plan declares fails where it is
consumed. Each member is an action (see
[`../language/actions.md`](../language/actions.md)).

`attach` and `capture` receive no terminal operand. `send_text` and `send_keys`
receive the literal text or key token as the action's first positional
argument.

Terminal commands are raw verbs. `send_text` sends literal text, `send_keys`
sends key-combo input, and `capture` returns terminal text for the consumer to
interpret. Agent-runtime plugins compose those verbs into submit/readiness
behavior — with one exception: starting the agent's own long-lived process,
covered next, is core's job, not the plugin's.

### Starting a Long-Lived Process

An effect that starts a long-lived agent process (a `claude` or `codex`
runtime, for instance) declares `[setup.process]` on its `setup` action
instead of hand-composing a launch line and sending it through `send_text`
and `send_keys` itself. This is the one composition step core owns on the
plugin's behalf, because it is a security boundary: nothing a plugin passes
into `[setup.process]` as `env` or `path` data can become part of the command
line typed into the pane.

```toml
[runtime.setup.process]
command = { from = "self.outputs.command" }

[runtime.setup.process.env]
PLECT_SESSION_NAME = { from = "session.name" }

[runtime.setup.process.path]
prepend = { from = "inputs.path_prepend", optional = true }
```

Core resolves `command`, `env`, and `path`, composes `env` into shell-quoted
`export` statements (rejecting any key that is not a valid environment
variable name), applies `path`'s `prepend` then `append` operations, appends
`command`, and submits the composed line into the plan's declared `[terminal]`
endpoint through its own `send_text` followed by `send_keys` (`Enter`) — once.
This runs as part of the `setup` action itself, after the action's own
`script`/`exec` body has produced the outputs `command`, `env`, and `path`
read from, and before `setup` returns: a later node's setup that depends on
this one may assume the process is already running. The full grammar and
validation rules are in [`../language/effects.md`](../language/effects.md).

Everything else about starting an agent — deriving a resume/fresh session id,
assembling an MCP config or hooks file, choosing model/effort flags, and
confirming the process actually came up — stays plugin-owned. `[setup.process]`
replaces only the launch-line construction and its terminal handoff.

### Claude and Codex, Rewritten

Both shipped agent-runtime effects carry the identical kernel this surface
retires: a `launch_env` JSON object parsed, key-validated, and shell-quoted
into `export` statements; a `path_prepend` input composed into `PATH`; the
result sent via `send_text` and `send_keys`. Eliding the parts of each effect
that do not change (resume branching, MCP/hooks assembly, model/effort flags,
the post-launch readiness poll — see
[`../adr/2026-08-24-effect-setup-process-substep.md`](../adr/2026-08-24-effect-setup-process-substep.md)
for where that poll moves), the `claude` runtime effect's setup becomes:

```toml
[runtime.setup]
type   = "shell"
script = '''
# Unchanged: derive SID/CLAUDE_ARG from prev_session_id, assemble the MCP
# config and socket path, write the hooks settings file, reset the activity
# probe, and build MODEL_FLAG/EFFORT_FLAG/DISALLOWED_FLAG.
#
# What is gone: the launch_env parse/validate/quote block, the path_prepend
# composition, and the send_text/send_keys handoff. What is added: the
# composed CLAUDE_CMD is emitted as a "command" output instead of being typed
# into the pane directly. pid moves from a required setup output to one the
# health.alive probe discovers and writes back (see the ADR).
jq -nc \
  --arg sid "$SID" --arg sock "$SOCKET" --arg mcp "$MCP" --arg hooks "$HOOKS" \
  --arg command "$CLAUDE_CMD" \
  '{session_id:$sid, socket_path:$sock, mcp_config:$mcp, hooks_settings:$hooks, command:$command}'
'''

[runtime.setup.bind]
session_name    = { from = "session.name" }
prev_session_id = { from = "prev.session_id", optional = true }
prev_pid        = { from = "prev.pid", optional = true }
model           = { from = "inputs.model", optional = true }
effort          = { from = "inputs.effort", optional = true }
mcp_servers     = { from = "inputs.mcp_servers", optional = true }
activity_bin    = { bin = "claude-agent-activity" }
mcp_servers_bin = { bin = "claude-mcp-servers" }

[runtime.setup.process]
command = { from = "self.outputs.command" }

[runtime.setup.process.env]
PLECT_SESSION_NAME = { from = "session.name" }
# One entry per launch_env key the caller supplies today; core rejects an
# invalid key name and never expands a value, so the effect's own
# inputs_schema pattern restrictions on model/effort (a plain-token charset)
# remain the defense for values that still reach the pane as part of the
# composed command text.

[runtime.setup.process.path]
prepend = { from = "inputs.path_prepend", optional = true }
```

`exec_runtime` (Codex headless) changes the same way: its `launch_env`
parse/quote block and `path_prepend` composition are deleted, its
`send_text`/`send_keys exec $worker_bin ...` line becomes a `command` output,
and `[setup.process]` submits it — `CODEX_MODEL` and
`CODEX_MODEL_REASONING_EFFORT` move from hand-built `export` lines in the
script into `[setup.process.env]` entries alongside `PLECT_SESSION_NAME` and
`PLECT_AGENT_ACTIVITY_BIN`. The interactive Codex TUI effect
(`codex.toml`) carries the same duplicated kernel; whether it is rewritten in
the same implementation change is a scope decision for that change, not this
design.

Node-inputs wiring above these effects — `path_prepend = { from =
"locals.guard_dir" }` on a workflow node, or a nested outer effect's
`[inner.inputs]` — is unaffected: `path_prepend` still flows in as an input,
it is simply consumed by `[setup.process.path]` instead of by the script's own
`PATH` composition.

### Validation

`command` is required and resolves to a string; `env` keys match
`^[A-Za-z_][A-Za-z0-9_]*$` and their resolved values, like `path.prepend` and
`path.append`, are scalars; `path` admits only the `prepend` and `append`
keys, and both may be present together. A `[setup.process]` declaration
requires some effect in the plan to declare `[terminal]`, the endpoint it
submits into, and at most one layer of a nesting chain may declare it — the
same per-chain shape `[terminal]` itself already has. The full rule set lives
in [`../language/effects.md`](../language/effects.md); this design adds no
rule beyond it, only the boundary-ownership fact that composing and
submitting the launch line is core's job, never a plugin script's.

### tmux Provider

```toml
# The effect also declares setup, cleanup, [health], and an
# interactive_endpoint output.

[pane.terminal.send_text]
type   = "shell"
script = 'tmux send-keys -t "$endpoint" -- "$1"'

[pane.terminal.send_text.bind]
endpoint = { from = "self.outputs.interactive_endpoint" }
```

The `interactive_endpoint` value is an opaque string. The tmux effect uses a
session name; a different multiplexer may use a pane id, socket path token, or
other implementation-owned binding.

### Codex Terminal Submit

`codex` owns Codex TUI submission because the burst split, readiness
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

# codex owns the burst split, readiness predicate, retry policy, and
# fail-loud behavior around these raw terminal calls.
sh -c "$send_text_cmd" terminal-send-text "$message"
sh -c "$send_keys_cmd" terminal-send-keys Enter
sh -c "$capture_cmd" terminal-capture
''',
  "terminal_submit",
  { terminal = "send_text" },
  { terminal = "send_keys" },
  { terminal = "capture" },
  { expr = "'[' + event.type + '] ' + (event.body != '' ? event.body : event.summary)" },
]
timeout = "45s"
```

The channel command is static. Resolved terminal capabilities and event data
ride in `args`, so event data can choose operands but not the executable.
The readiness predicate and retry details live in the shipped `codex`
config.

Claude Code delivery uses channel-server as its structured delivery path. A
no-channel-server interactive Claude configuration is outside the supported
surface. Raw terminal submit is the fallback for interactive TUIs without
structured transport.

### Herdr Provider

```toml
# The effect returns an opaque Herdr pane id as interactive_endpoint.

[pane.terminal.send_text]
type   = "shell"
script = 'herdr pane.send_text "$endpoint" "$1"'

[pane.terminal.send_text.bind]
endpoint = { from = "self.outputs.interactive_endpoint" }
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

1. `slack` receives a Slack message and publishes
   `conversation.message` with opaque Slack correlation metadata.
2. `claude` consumes `conversation.message` through its structured
   channel-server delivery path, or `codex` consumes the same event
   through its terminal-submit channel.
3. The agent runtime publishes `conversation.reply` or
   `conversation.permission_request`.
4. `slack` maps those events back to the bound Slack thread.
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
