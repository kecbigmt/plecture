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
task declares an opaque `interactive_endpoint` output and a `[terminal]` table
whose terminal verbs act on that endpoint: `attach`, `capture`, `send_text`,
and `send_keys`. Universal task keys stay top-level and unchanged: `setup`
declares endpoint creation by returning the `interactive_endpoint` binding,
`cleanup` releases multiplexer-owned resources, and `healthcheck` reports
whether the endpoint is live enough for delivery.

`healthcheck` is not terminal-specific. A Claude task may use the same
universal key for process-id self-healing; a multiplexer task's healthcheck is
only that universal key's terminal-surface instance. `interactive_endpoint` is
the binding terminal verbs target; it is not a new top-level config kind.

A task with a `[terminal]` table provides the terminal operation surface. The
table must declare all four members: `attach`, `capture`, `send_text`, and
`send_keys`. A partial terminal surface is a load error.

The `[terminal]` table makes the terminal surface visible in the config shape
itself. It also aligns the config layer with the `{{terminal "send_text"}}`
template helper and the Terminal Operation Surface vocabulary, so one word
carries through the config shape, template call site, and prose contract.

Terminal operations are invoked by template injection, generalizing the
existing `{{bin "..."}}` helper. `{{terminal "send_text"}}`,
`{{terminal "send_keys"}}`, `{{terminal "capture"}}`, and the other terminal
operation templates ask plect to resolve the session's selected multiplexer
task operation into the concrete command line declared by that task. The helper
is available to consumer task hooks and channel arguments; channel command
fields stay static, and injected command lines ride in rendered arguments.
Making the helper available in channel arguments deliberately extends the
channel template function set beyond its existing event-serialization helpers
while preserving the invariant that channel `command` is verbatim and event or
input data can never choose the executable. Agent runtime plugins and channels
therefore contain no tmux commands, no tmux output field knowledge, and no
concrete multiplexer plugin references.

Plecture adds no CLI commands for terminal operations. Consumers must be
declared task hooks or event channels, which keeps pane input inside the
session's declared workflow surface. Manual debugging continues to use the
existing `plect attach` and `plect capture` commands.

The send and capture operations are raw terminal verbs. Agent-runtime plugins
own submit and readiness composition for their interactive TUIs: rendering the
message, sending literal text, sending submit keys, checking prompt/readiness
through capture, and retrying when the runtime treats a nearby Enter as a
newline instead of submission. Initial-prompt tasks therefore belong to the
agent runtime plugin whose TUI receives the prompt, not to the multiplexer
plugin. A terminal-submit event channel for an interactive TUI belongs to the
agent runtime plugin as well.

The existing tmux text-input channel splits along this contract when the split
is implemented: its tmux command lines move behind injected terminal operation
commands, while burst splitting, prompt-glyph and non-breaking-space readiness
checks, backoff, and fail-loud behavior move into `session/codex`'s
terminal-submit composition.

When an agent runtime has a structured delivery channel, that channel is the
supported event-delivery path because it is more robust than terminal key
submission. Claude Code delivery therefore uses channel-server, and a
no-channel-server interactive Claude configuration is outside this decision's
supported surface. Raw-verb terminal submit is the fallback for interactive
TUIs without structured transport, such as Codex interactive.

Moving `attach` and `capture` from top-level task keys into `[terminal]` is a
breaking config change. The split implementation PR carries this change for
the shipped tmux task and includes a `docs/migrations/` procedure for
user-owned multiplexer tasks or local overlays that declare top-level `attach`
or `capture`. It also updates `plect attach` and `plect capture` resolution to
read those declarations from `[terminal]`. Folding this shape into the
already-open breaking window keeps the one-time migration machinery hot.
Deferring would let the flat shape ossify before the terminal surface has a
visible config boundary.

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

Implementation work includes defining the `[terminal]` task table and its
all-or-nothing load validation, migrating shipped top-level `attach` and
`capture` declarations into `[terminal]`, updating `plect attach` and
`plect capture` to resolve `[terminal].attach` and `[terminal].capture`, adding
a `docs/migrations/` entry for user-owned top-level `attach` and `capture`
declarations, adding the terminal operation template helper to task hooks and
channel argument rendering, keeping `interactive_endpoint` as the opaque
operation binding, moving initial-prompt and terminal-submit composition into
the agent runtime plugins, adding the provider-neutral conversation event
vocabulary, moving Slack delivery off channel-server socket subscriptions,
moving the channel-server socket protocol out of `contracts/` when it has no
cross-plugin consumer, splitting the current session runtime package into the
selected plugins, dropping the `plect-` prefix from the session-runtime plugin
executables when they move into `session/claude` and `session/codex`, moving
`gh-guard` into the GitHub plugin, and adding a `docs/migrations/` entry for
no-channel-server interactive Claude configurations.

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
carry setup, cleanup, healthcheck, attach, and capture commands. That is enough
for one local workflow overlay that edits the producer node, the consumer
templates, and any agent-runtime submit/readiness config together.

It is not enough for a reusable plugin split. The output name is a convention
owned by the concrete task, not a declared core capability. A consumer plugin
has no stable way to say it needs an attachable, capturable, input-receiving
terminal endpoint without naming the concrete producer's node id or output
shape.

### Opaque endpoint binding without operations

An opaque handle alone also loses. If the only common contract is
`interactive_endpoint`, the plugin or channel that types into it still needs
tool-specific command knowledge. That recreates the dependency the split is
meant to remove. The reusable seam is therefore the task-declared operation
surface, with `interactive_endpoint` kept only as the binding those operations
target.

### Name the template helper `op` or `operation`

`{{op "send_text"}}` is rejected because abbreviations are not part of the
contract vocabulary. `{{operation "send_text"}}` is rejected because it does
not name the terminal operation surface and would collide conceptually with
other operation-shaped concepts later. `{{terminal_operation "send_text"}}` is
rejected because it repeats the surface name without adding disambiguating
meaning. `{{terminal "send_text"}}` keeps the helper in the terminal operation
vocabulary and follows the literal-string convention already used by
`{{bin "..."}}`.

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
the interactive terminal-submit event channel with `session/codex`. Claude Code
does not get a terminal-submit event channel in this split because its
structured channel-server delivery is the supported path.

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
