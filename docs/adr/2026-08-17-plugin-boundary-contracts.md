---
supersedes: 2026-08-16-plugin-service-lifecycle
---

# Plugin boundary contracts and event rendezvous

## Context

This decision is specified by
[`docs/design/plugin-boundary-contracts.md`](../design/plugin-boundary-contracts.md)
and
[`docs/design/plugin-packaging.md`](../design/plugin-packaging.md).

The owner wants the plugin set to serve as building blocks for team workflows.
The first forcing workflow is review request, review session, team conversation
thread, conversational review, and AWS-hosted orchestration.

The service-lifecycle decision made plugin-owned daemons first-class, but its
Plugin Boundary Rule treated an apparent dependency edge as a boundary smell
whose first remedy was merging the plugins that shared a runtime contract. That
rule made sense while the problem was adopting resident daemons, but it is too
coarse for reusable workflow blocks. A single session-runtime plugin couples
unrelated choices: a terminal multiplexer, an agent CLI, structured agent
delivery, chat delivery, and a GitHub CLI write guard.

The split must not replace the merged plugin with plugin-to-plugin contracts.
Core still must not know that any specific provider, chat service, VCS, agent
CLI, or terminal multiplexer exists.

## Decision

Plecture keeps first-class plugin service declarations. `plugin.toml` supports
`[[services]]` entries for bus-supervised plugin daemons. The bus supervisor
starts declared services when the bus starts, restarts crashed children with
bounded backoff, records service status, restarts services when their plugin
content changes, and stops services when the bus stops.

Service declarations are provider-agnostic. A declaration names a plugin-owned
executable, arguments, environment/config bindings, readiness or health checks,
restart policy, and log policy. Catalog content does not carry secrets; tokens,
credentials, and similar values stay in user configuration, environment, or
environment files.

Plecture replaces the merge-first boundary rule with a core-contract boundary
rule: when a reusable runtime contract would cross a plugin boundary, the
contract either becomes provider-neutral core structure or the participants
rendezvous through the event bus. A plugin boundary may not depend on another
plugin's private package, executable, config shape, or provider-specific event
schema.

The multiplexer seam is a core-owned terminal operation surface. A multiplexer
task declares an opaque `interactive_endpoint` output and terminal operations
that create or act on it: `create`, `attach`, `capture`, `send_text`,
`send_keys`, `healthcheck`, and `cleanup`. `interactive_endpoint` is the
binding those operations target; it is not a new top-level config kind. Agent
runtime plugins and channels call the operations through plect's common
surface, so they contain no tmux commands, no tmux output field knowledge, and
no concrete multiplexer plugin references.

`create` and `cleanup` are the existing task `setup` and `cleanup` hooks viewed
through this operation contract, not new parallel task fields. `attach`,
`capture`, and `healthcheck` are the existing task-level declarations. The new
operation declarations are `send_text` and `send_keys`.

The send and capture operations are raw terminal verbs. Agent-runtime plugins
own submit and readiness composition for their interactive TUIs: rendering the
message, sending literal text, sending submit keys, checking prompt/readiness
through capture, and retrying when the runtime treats a nearby Enter as a
newline instead of submission. Initial-prompt tasks therefore belong to the
agent runtime plugin whose TUI receives the prompt, not to the multiplexer
plugin. A terminal-submit event channel for an interactive TUI belongs to the
agent runtime plugin as well.

Chat delivery and agent delivery rendezvous through provider-neutral
conversation events on the Plecture event bus:
`conversation.message`, `conversation.reply`,
`conversation.permission_request`, and `conversation.permission_reply`.
Chat plugins map concrete chat systems to those events. Agent runtime plugins
consume and publish those events. The channel-server socket protocol is not
the chat-to-agent boundary. After Slack delivery moves to event-bus
rendezvous, the channel-server socket protocol moves out of shared
`contracts/` ownership and belongs with the structured agent runtime
implementation that uses it.

The reusable session runtime surface splits into:

- `session/tmux`;
- `session/claude`;
- `session/codex`;
- `slack-delivery`;
- `github`.

`gh-guard` belongs in the `github` plugin because it is a GitHub CLI policy
shim. Agent runtime plugins accept only generic environment or path inputs;
they do not expose GitHub-specific guard flags.

Team review workflows stay configuration-level composition. A reusable review
workflow may be distributed as a configuration-only plugin, but no executable
review-workflow plugin is required by this decision.

## Consequences

Only the `plect bus serve` unit remains host-owned. Plugin daemons become part
of the mounted plugin surface and are supervised by the bus.

The `github` plugin declares a `github-watcher` service. `session/claude`
declares the `channel-server` service. `slack-delivery` declares the
`slack-adapter` service.

Service state is bus-global and includes service identity, running state, pid,
restart count, last exit, last error, last health result, plugin id, and the
plugin lock coordinate or content hash that produced the running process.

Core needs manifest parsing and validation for `[[services]]`, a bus-owned
service supervisor, a service status model, and plugin update/remove signaling
or lockfile polling. It does not need plugin dependency parsing, dependency
closure checks, capability matching, or version solving.

Plugin splitting remains possible without adding plugin dependency metadata,
capability solving, or cross-plugin executable references.

Core grows only where a contract is durable across concrete technologies:
terminal operations, opaque interactive endpoint bindings, and conversation
events. Concrete multiplexer, agent, chat, and VCS behavior remains in plugins.

The official catalog can offer smaller independently selectable packages. An
operator can select a different multiplexer, choose Claude or Codex runtime
support independently, use Slack delivery without Claude-specific socket
knowledge, and use the GitHub guard without installing it as session-runtime
behavior.

Implementation work includes defining the task-level terminal operation
surface, keeping `interactive_endpoint` as the opaque operation binding, moving
initial-prompt and terminal-submit composition into the agent runtime plugins,
adding the provider-neutral conversation event vocabulary, moving Slack
delivery off channel-server socket subscriptions, moving the channel-server
socket protocol out of `contracts/` when it has no cross-plugin consumer,
splitting the current session runtime package into the selected plugins, and
moving `gh-guard` into the GitHub plugin.

This decision supersedes the plugin service lifecycle decision by carrying
forward its service declaration and bus-supervision decisions while replacing
its merge-first plugin-boundary remedy.

## Alternatives considered

### Keep the merged session-runtime plugin

Keeping one package avoids new core contracts, but it makes a user install a
whole runtime stack to get any one part of it. It also prevents the first team
review workflow from choosing another multiplexer or using chat delivery as a
general event-bus participant.

### Plugin-to-plugin dependencies

Plugin dependency metadata could let one plugin name another plugin or require
one of its capabilities. This loses for the same reason recorded in the
service-lifecycle decision: identity edges hide the contract being consumed and
pull Plecture toward version solving before there is a proven registry problem.

### Config-only multiplexer composition

Workflow config can already connect a tmux task to an agent task by passing a
provider-neutral output such as `session_name`, and task declarations already
carry attach, capture, and healthcheck commands. That is enough for one local
workflow overlay that edits the producer node, the consumer templates, and any
agent-runtime submit/readiness config together.

It is not enough for a reusable plugin split. The output name is a convention
owned by the concrete task, not a declared core capability. A consumer plugin
has no stable way to say it needs an attachable, capturable, input-receiving
terminal endpoint without naming the concrete producer's node id or output
shape.

An opaque handle alone also loses. If the only common contract is
`interactive_endpoint`, the plugin or channel that types into it still needs
tool-specific command knowledge. That recreates the dependency the split is
meant to remove. The reusable seam is therefore the task-declared operation
surface, with `interactive_endpoint` kept only as the binding those operations
target.

### Require Herdr's richer multiplexer API

The terminal operation surface was checked against tmux and
[Herdr](https://github.com/herdrdev/herdr), using Herdr's documented
[socket API](https://herdr.dev/docs/socket-api/) as a real second
implementation. The required contract maps cleanly to both: tmux identifies an
endpoint by session name, while Herdr identifies one by pane id such as `w1:p1`
and supplies `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` as operation context; the
binding is therefore an opaque string, never a tmux-shaped name.

Herdr separates literal text input from key-combo input with `pane.send_text`
and `pane.send_keys`; tmux implements both contract verbs with `send-keys`.
Keeping both `send_text` and `send_keys` in the required contract preserves
that semantic difference without requiring consumers to know which multiplexer
is selected.

Herdr also offers semantic agent status, `agent.wait --until`, and
`events.subscribe`. Those are useful extension capabilities, and readiness
polling such as an initial prompt may use them when declared. They are not part
of the required surface because tmux can provide the portable terminal verbs
without providing semantic agent lifecycle events.

### Put submit and readiness composition in the multiplexer plugin

The tmux-backed implementation already contains submit/readiness logic next to
tmux commands because there was only one package. Keeping that logic in the
multiplexer plugin after the split would make the multiplexer know agent-TUI
details such as prompt glyphs, paste-burst behavior, and retry schedules. That
would replace the original agent-to-tmux dependency with the opposite
tmux-to-agent dependency.

The multiplexer contract therefore stops at raw terminal operations. The agent
runtime plugin composes those operations into the submit/readiness behavior its
own TUI requires. This places Claude initial-prompt composition with
`session/claude`, Codex initial-prompt composition with `session/codex`, and
the interactive terminal-submit event channel with `session/codex`.

### Keep Slack delivery on the channel-server socket protocol

Keeping the socket protocol as the Slack-to-agent boundary preserves the
shortest implementation path, but it couples chat delivery to one agent
runtime shape. Team workflows need the team conversation to address any
session runtime that understands Plecture conversation events.

### Make `gh-guard` standalone

A standalone guard plugin would isolate the guard from the rest of GitHub
resource observation, but it would package exactly one GitHub CLI policy shim.
The write-it-twice threshold for a neutral VCS guard is not met. If a second
VCS guard appears, a later decision can extract a provider-neutral write-guard
contract or a separate guard package.
