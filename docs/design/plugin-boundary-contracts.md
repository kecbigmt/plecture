# Plugin boundary contracts

This design is governed by
[`../adr/2026-08-17-plugin-boundary-contracts.md`](../adr/2026-08-17-plugin-boundary-contracts.md).

## Boundary Rule

Plugin boundaries are package boundaries, not runtime-isolation boundaries.
A plugin may collaborate with another plugin only through these boundary
surfaces:

- core-owned workflow, task, channel, resource, workspace, and template composition;
- core-owned provider-neutral Go contracts under `contracts/`;
- core-owned provider-neutral event-bus types;
- opaque plugin-owned event types that other plugins treat as uninterpreted payloads.

Shipped plugin configuration references only executables declared by the same
plugin. User-owned workflow and task overlays compose definitions from multiple
plugins by id.

Core owns durable work structure: identity, lifecycle, relationships,
observation, verification, and handoff. Plugins own concrete commitments to
tools, providers, communication systems, and agent runtimes.

## Session Runtime Plugins

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
resources is still a plugin. A team may publish a reusable review workflow as
a configuration-only plugin, but the workflow itself is composition, not a
required executable adapter.

## Terminal Operation Surface

The multiplexer seam is a core-owned terminal operation surface.

A multiplexer task declares an `interactive_endpoint` output and the terminal
operations that create or act on it. `interactive_endpoint` is an opaque string
binding that identifies the live endpoint for plect. It is not a new top-level
config kind, and it is not sufficient without the declared operations.

An interactive endpoint represents a live text terminal attached to a session.
It is not a tmux pane, a process id, a worktree, an agent runtime, or a chat
thread. A tmux-backed endpoint may use a tmux session name as its binding. A
[Herdr](https://github.com/herdrdev/herdr)-backed endpoint may use a pane id
such as `w1:p1`, with Herdr's `HERDR_SOCKET_PATH` and `HERDR_PANE_ID`
environment values supplied as operation context, as documented by the
[Herdr socket API](https://herdr.dev/docs/socket-api/). Consumers treat both
bindings as opaque strings.

The terminal operation set is:

| Operation | Meaning |
|---|---|
| `create` | Create the endpoint and return its `interactive_endpoint` binding |
| `attach` | Attach the user's terminal to the endpoint |
| `capture` | Return the endpoint's current transcript |
| `send_text` | Send literal text input to the endpoint |
| `send_keys` | Send key-combo input to the endpoint |
| `healthcheck` | Report whether the endpoint is live enough for delivery |
| `cleanup` | Close the endpoint and release multiplexer-owned resources |

The existing task-level `attach`, `capture`, and `healthcheck` declarations
are members of this operation set. Existing task `setup` declares `create`, and
existing task `cleanup` declares `cleanup`; they are not new parallel config
keys. `send_text` and `send_keys` extend the same task-declared operation model
to delivery while preserving the semantic difference between literal text and
key-combo input.

The required operations map to concrete multiplexers without leaking those
tools into consumers:

| Operation | tmux mapping | Herdr mapping |
|---|---|---|
| `create` | create the tmux session | `workspace.create` and `pane.split` |
| `attach` | attach to the tmux session | reattach to the Herdr session or pane |
| `capture` | capture pane output | `pane.read` with `--source` and `--lines` |
| `send_text` | `send-keys` with literal text | `pane.send_text` |
| `send_keys` | `send-keys` with key tokens | `pane.send_keys` |
| `healthcheck` | check the tmux session and pane | `agent.get` or pane existence |
| `cleanup` | kill the tmux session or pane | `pane.close` |

Agent runtime plugins and channels call these operations through terminal
operation template injection. `{{terminal "send_text"}}`,
`{{terminal "send_keys"}}`, `{{terminal "capture"}}`, and the other terminal
operation templates resolve the session's selected multiplexer task operation
into the concrete command line declared by that task. The helper is available
to consumer task hooks and channel arguments, following the same literal-name
template convention as `{{bin "..."}}`. Channels keep a static command and
receive rendered terminal operation command lines through their arguments, so
args-only channel rendering remains sufficient.

Plecture does not expose CLI commands for terminal operations. Terminal
operation consumers are declared task hooks and event channels only. Manual
debugging stays on the existing `plect attach` and `plect show --capture`
commands.

Agent runtime plugins and channels do not invoke multiplexer commands, inspect
multiplexer-specific output fields, or name a concrete multiplexer plugin.

Terminal operations are raw verbs. They do not define an agent submit protocol,
readiness predicate, prompt-glyph vocabulary, retry schedule, or burst-splitting
policy. An agent-runtime plugin that drives an interactive TUI owns that
composition: it renders the message, calls `send_text` for literal text, calls
`send_keys` for submit keys such as Enter, calls `capture` to evaluate the
runtime's prompt/readiness state, and retries according to that runtime's TUI
behavior.

The initial-prompt task belongs to the agent runtime plugin whose TUI receives
the prompt. `session/claude` and `session/codex` each ship an initial-prompt
task that composes the raw terminal verbs with that runtime's readiness checks.
`session/claude` uses channel-server as its supported event-delivery path
because structured delivery is more robust than terminal key submission. A
no-channel-server interactive Claude configuration is outside this supported
surface. `session/codex` ships a terminal-submit event channel for its
interactive TUI shape because Codex interactive has no structured transport.
`session/tmux` does not ship agent-TUI submit or readiness composition.

`session/codex`'s terminal-submit channel composes raw terminal operations into
Codex TUI delivery. The multiplexer command positions are injected terminal
operation command lines: literal message delivery uses
`{{terminal "send_text"}}`, submit-key delivery uses
`{{terminal "send_keys"}}`, and verification uses `{{terminal "capture"}}`.
The burst split, prompt-glyph and non-breaking-space readiness predicate,
backoff schedule, and fail-loud behavior belong to `session/codex` because they
describe the Codex TUI submit contract, not the multiplexer.

The operation surface lets a workflow swap one multiplexer implementation for
another by replacing the producer node. Agent plugins depend on the core
operation contract and the `interactive_endpoint` binding, not on the concrete
multiplexer plugin.

Richer multiplexers may expose optional capabilities outside the required
operation set. Herdr exposes semantic agent status such as `working`,
`blocked`, `idle`, and `done`; readiness waits such as `agent.wait --until`;
and event subscriptions such as `events.subscribe`. Plecture workflows may use
those extension capabilities when a selected multiplexer declares them, such as
using `agent.wait` for initial-prompt readiness. The required-surface path uses
the portable `capture` operation plus the agent runtime's prompt/readiness
predicate.

## Conversation Events

Human conversation and agent replies rendezvous on the Plecture event bus.
Delivery plugins map concrete communication systems to provider-neutral
conversation events. Agent-runtime plugins consume and publish those same
events.

Core owns these conversation event types:

| Type | Direction | Meaning |
|---|---|---|
| `conversation.message` | inbound | A human-authored or operator-authored message addressed to the session |
| `conversation.reply` | outbound | Agent-authored text for the active conversation |
| `conversation.permission_request` | outbound | A runtime asks the human conversation for a decision |
| `conversation.permission_reply` | inbound | A human decision for a permission request |

Conversation events carry opaque metadata for conversation identity,
participant identity, and delivery correlation. Core records and routes those
fields without interpreting a concrete communication provider.

The channel-server socket protocol is an implementation protocol for a
structured agent runtime. It is not the boundary between chat delivery and
agent delivery. Chat-delivery plugins do not connect to channel-server sockets
and do not import channel-server client packages.

The channel-server socket protocol belongs with the structured agent runtime
implementation that uses it. It is not a shared `contracts/` package unless
another concrete consumer needs the same provider-neutral wire contract.

## Review Workflow Composition

The team review workflow is configuration-level composition:

1. a resource definition observes the review request;
2. a workspace provider acquires the work surface;
3. a workflow starts a selected multiplexer endpoint and a selected agent
   runtime;
4. a delivery plugin binds the team conversation and maps messages through the
   event bus;
5. `done_when`, judge, and terminal events record verification and handoff.

No review-workflow executable plugin is required. A shared review workflow may
be distributed as a configuration-only plugin when reuse justifies packaging,
or it may remain user-owned workflow and template configuration.

## GitHub CLI Guard

The GitHub CLI write guard belongs to the `github` plugin. It is a concrete
GitHub CLI adapter and policy shim, not session-runtime behavior.

Workflows that need the guard compose the GitHub plugin's guard task or
environment output as an upstream runtime input. Agent plugins accept only
generic environment or path inputs; they do not carry a GitHub-specific guard
switch.
