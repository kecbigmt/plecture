# Plugin boundary contracts

This design is governed by
[`../adr/2026-08-17-plugin-boundary-contracts.md`](../adr/2026-08-17-plugin-boundary-contracts.md).

## Boundary Rule

Plugin boundaries are package boundaries, not runtime-isolation boundaries.
A plugin may collaborate with another plugin only through surfaces owned by
Plecture core:

- workflow, task, channel, resource, workspace, and template composition;
- provider-neutral Go contracts under `contracts/`;
- provider-neutral event-bus types whose schema is owned by core;
- opaque plugin-owned event types that other plugins do not interpret.

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
| `session/tmux` | tmux-backed interactive endpoint task and terminal operation declarations | interactive endpoint, terminal operations, task lifecycle | agent CLIs, chat delivery, VCS guards |
| `session/claude` | Claude Code launch tasks, Claude Code delivery, channel-server service, Claude activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Codex, chat-service adapters, VCS guards |
| `session/codex` | Codex TUI and `codex exec` launch tasks, queue worker, enqueue channel, Codex activity hook | interactive endpoint, terminal operations, conversation events, task lifecycle | tmux, Claude, chat-service adapters, VCS guards |
| `slack-delivery` | Slack adapter service, Slack thread binding, Slack event ingress and egress | conversation events, plugin services, channel delivery | agent runtimes, channel-server sockets, VCS guards |
| `github` | GitHub resource observation, workspace acquisition, watcher service, GitHub CLI write guard | resource definitions, workspace providers, subscriptions, plugin services | session runtime tasks, chat-service adapters |

A plugin with only workflow, task, channel, template, or other configuration
resources is still a plugin. A team may publish a reusable review workflow as
a configuration-only plugin, but the workflow itself is composition, not a
required executable adapter.

## Terminal Operation Surface

The multiplexer seam is a core-owned terminal operation surface.

A multiplexer task declares an `interactive_endpoint` output and the terminal
operations that act on it. `interactive_endpoint` is the task-output binding
that identifies the live endpoint for plect. It is not a new top-level config
kind, and it is not sufficient without the declared operations.

An interactive endpoint represents a live text terminal attached to a session.
It is not a tmux pane, a process id, a worktree, an agent runtime, or a chat
thread.

The terminal operation set is:

| Operation | Meaning |
|---|---|
| `attach` | Attach the user's terminal to the endpoint |
| `capture` | Return the endpoint's current transcript |
| `send_input` | Send text input to the endpoint |
| `healthcheck` | Report whether the endpoint is live enough for delivery |

The existing task-level `attach`, `capture`, and `healthcheck` declarations
are members of this operation set. `send_input` extends the same task-declared
operation model to text delivery.

Agent runtime plugins and channels call these operations through plect's common
surface. They do not invoke multiplexer commands, inspect multiplexer-specific
output fields, or name a concrete multiplexer plugin. The shipped text-input
channel is generic and invokes `send_input` on the session's declared
multiplexer endpoint.

The operation surface lets a workflow swap one multiplexer implementation for
another by replacing the producer node. Agent plugins depend on the core
operation contract and the `interactive_endpoint` binding, not on the concrete
multiplexer plugin.

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
