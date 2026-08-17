# Plugin boundary contracts and event rendezvous

## Context

This decision is specified by
[`docs/design/plugin-boundary-contracts.md`](../design/plugin-boundary-contracts.md).

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

Plecture replaces the merge-first boundary rule with a core-contract boundary
rule: when a reusable runtime contract would cross a plugin boundary, the
contract either becomes provider-neutral core structure or the participants
rendezvous through the event bus. A plugin boundary may not depend on another
plugin's private package, executable, config shape, or provider-specific event
schema.

The multiplexer seam is a core-owned `interactive_endpoint` task-output
contract. It is not a new top-level config kind. A multiplexer task declares an
interactive endpoint in its output schema and produces that endpoint through
the existing workflow node-output mechanism. Agent runtime plugins consume it
as an opaque text-terminal handle, so a workflow can substitute another
multiplexer implementation without changing the agent plugin.

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

Plugin splitting remains possible without adding plugin dependency metadata,
capability solving, or cross-plugin executable references.

Core grows only where a contract is durable across concrete technologies:
interactive endpoints and conversation events. Concrete multiplexer, agent,
chat, and VCS behavior remains in plugins.

The official catalog can offer smaller independently selectable packages. An
operator can select a different multiplexer, choose Claude or Codex runtime
support independently, use Slack delivery without Claude-specific socket
knowledge, and use the GitHub guard without installing it as session-runtime
behavior.

Implementation work includes defining the task-level `interactive_endpoint`
output contract, adding the provider-neutral conversation event vocabulary,
moving Slack delivery off channel-server socket subscriptions, moving the
channel-server socket protocol out of `contracts/` when it has no cross-plugin
consumer, splitting the current session runtime package into the selected
plugins, and moving `gh-guard` into the GitHub plugin.

The existing plugin service lifecycle decision still governs `[[services]]`
and bus supervision. This decision replaces only its plugin-boundary remedy.

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
workflow overlay that edits the producer node, the consumer templates, and the
producer-owned delivery channel together.

It is not enough for a reusable plugin split. The output name is a convention
owned by the concrete task, not a declared core capability. A consumer plugin
has no stable way to say it needs an attachable, capturable, text-sendable
terminal endpoint without naming the concrete producer's node id or output
shape. Config-only composition therefore describes the operator motion for
choosing a multiplexer, but it does not carry the published swap guarantee
between independently distributed plugins.

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
