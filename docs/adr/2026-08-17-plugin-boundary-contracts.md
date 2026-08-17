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

Plecture keeps first-class plugin service declarations from the superseded
service-lifecycle decision. `plugin.toml` continues to support bus-supervised
`[[services]]` entries for plugin-owned daemons, with provider-agnostic
declarations and user-owned secret material.

Plecture replaces the merge-first boundary rule with a core-contract boundary
rule: when a reusable runtime contract would cross a plugin boundary, the
contract either becomes provider-neutral core structure or the participants
rendezvous through the event bus. A plugin boundary may not depend on another
plugin's private package, executable, config shape, or provider-specific event
schema.

The multiplexer seam becomes the Terminal Operation Surface specified by the
design note. Core owns the reusable terminal contract, while concrete tmux,
Herdr, or other multiplexer behavior stays in multiplexer plugins. The terminal
contract uses an opaque endpoint binding plus raw terminal verbs; agent-runtime
plugins own submit and readiness composition.

Terminal operations are invoked by template injection through
`{{terminal "..."}}`, not by new operation CLI commands. The helper is available
to task hooks and channel arguments. Channel command fields stay static, so
event and input data can choose operands but cannot choose the executable.

The terminal-only verbs live under the task's `[terminal]` config table. The
design note is the schema home for that table. The table shape is chosen because
terminal verbs are meaningful only for tasks that own an interactive endpoint,
and because the same word names the config table, template helper, and prose
contract.

When an agent runtime has a structured delivery channel, that channel is the
supported event-delivery path because it is more robust than terminal key
submission. Raw terminal submit is the fallback for interactive TUIs without
structured transport.

Chat delivery and agent delivery rendezvous through provider-neutral
conversation events on the Plecture event bus. The channel-server socket
protocol is not the chat-to-agent boundary; it belongs with the structured agent
runtime implementation that uses it unless another concrete consumer needs the
same provider-neutral wire contract.

The reusable session runtime surface splits into smaller plugins for the
multiplexer, agent runtimes, chat delivery, and GitHub behavior, with ownership
and exclusions specified in the design note. `gh-guard` belongs in the GitHub
plugin because it is a GitHub CLI policy shim. Team review workflows remain
configuration-level composition; no executable review-workflow plugin is
required by this decision.

## Consequences

Only the `plect bus serve` unit remains host-owned. Plugin daemons become part
of the mounted plugin surface and are supervised by the bus.

Service state is bus-global and includes service identity, running state, pid,
restart count, last exit, last error, last health result, plugin id, and the
plugin lock coordinate or content hash that produced the running process.

Core needs manifest parsing and validation for `[[services]]`, a bus-owned
service supervisor, a service status model, and plugin update/remove signaling
or lockfile polling. It does not need plugin dependency parsing, dependency
closure checks, capability matching, or version solving.

Core grows only where a contract is durable across concrete technologies:
terminal operations, opaque interactive endpoint bindings, and conversation
events. Concrete multiplexer, agent, chat, and VCS behavior remains in plugins.

Moving terminal verbs into `[terminal]` is a breaking config change for
user-owned multiplexer tasks or local overlays that declare top-level `attach`
or `capture`. The split implementation PR carries the shipped tmux task change,
updates `plect attach` and `plect capture` resolution, and adds a
`docs/migrations/` procedure for user-owned config.

The split implementation PR also adds the terminal operation template helper to
task hooks and channel argument rendering, moves initial-prompt and
terminal-submit composition into the agent runtime plugins, adds the
provider-neutral conversation event vocabulary, moves Slack delivery off
channel-server socket subscriptions, moves the channel-server socket protocol
out of `contracts/` when it has no cross-plugin consumer, splits the current
session runtime package into the selected plugins, drops the `plect-` prefix
from the session-runtime plugin executables when they move into
`claude` and `codex`, moves `gh-guard` into the GitHub plugin,
and adds a `docs/migrations/` entry for no-channel-server interactive Claude
configurations.

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

Workflow config can already connect a multiplexer task to an agent task by
passing a provider-neutral-looking output, and task declarations already carry
lifecycle and terminal-like command hooks. That is enough for one local workflow
overlay that edits the producer node, consumer templates, and agent-runtime
submit/readiness config together.

It is not enough for a reusable plugin split. The output name is a convention
owned by the concrete task, not a declared core capability. A consumer plugin
has no stable way to say it needs an attachable, capturable, input-receiving
terminal endpoint without naming the concrete producer's node id or output
shape.

### Opaque endpoint binding without operations

An opaque handle alone loses. If the only common contract is the endpoint
binding, the plugin or channel that types into it still needs tool-specific
command knowledge. That recreates the dependency the split is meant to remove.

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
implementation. The required contract maps cleanly to both and keeps tmux
first-class.

Herdr's richer semantic agent status, readiness waits, and event subscriptions
are useful extension capabilities. They are not part of the required surface
because tmux can provide portable terminal operations without providing
semantic agent lifecycle events.

### Group runtime plugins under `session/`

Grouping the split plugins under `session/` is rejected because `session`
discriminates nothing in this catalog: every plugin serves sessions. The group
would mix different kinds of plugin, such as a terminal multiplexer and agent
runtimes, even though dissolving their shared runtime contract is the point of
this decision.

Category directories are a second taxonomy to maintain and have already churned
plugin ids. Flat ids such as `official/claude` and `official/tmux` are
unambiguous because catalog entries are curated and enumerated. Classification
lives in the design note's plugin table, not the filesystem. This can be
revisited if the curated catalog grows beyond about twenty plugins.

### Put submit and readiness composition in the multiplexer plugin

The tmux-backed implementation contains submit/readiness logic next to tmux
commands because there was only one package. Keeping that logic in the
multiplexer plugin after the split would make the multiplexer know agent-TUI
details such as prompt glyphs, paste-burst behavior, and retry schedules. That
would replace the original agent-to-tmux dependency with the opposite
tmux-to-agent dependency.

The multiplexer contract therefore stops at raw terminal operations. The agent
runtime plugin composes those operations into the submit/readiness behavior its
own TUI requires. Claude Code does not get a terminal-submit event channel in
this split because its structured channel-server delivery is the supported path.

### Keep Slack delivery on the channel-server socket protocol

Keeping the socket protocol as the Slack-to-agent boundary preserves the
shortest implementation path, but it couples chat delivery to one agent runtime
shape. Team workflows need the team conversation to address any session runtime
that understands Plecture conversation events.

### Make `gh-guard` standalone

A standalone guard plugin would isolate the guard from the rest of GitHub
resource observation, but it would package exactly one GitHub CLI policy shim.
The write-it-twice threshold for a neutral VCS guard is not met. If a second
VCS guard appears, a later decision can extract a provider-neutral write-guard
contract or a separate guard package.
