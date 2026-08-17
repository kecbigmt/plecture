---
superseded_by: 2026-08-17-plugin-boundary-contracts
---

# Plugin service lifecycle

## Context

This decision is specified by
[`docs/design/plugin-packaging.md`](../design/plugin-packaging.md).

Catalog plugins can ship executables and configuration, but long-running
plugin-owned processes have not had a Plecture-owned lifecycle boundary.
`github-watcher` is shipped by the `github` plugin as an executable, while
`github-watcher serve` has been run as deployment-specific host service
configuration. `slack-adapter` is a standalone companion module, while
`channel/slack` only ships the event channel that posts to a running adapter
HTTP API.

`plect bus serve` starts core-owned followers such as the event dispatcher and
tick reactor. Those followers reconcile session state; they are not plugin
daemon supervisors.

`channel-server` is one per Claude Code session, not one global daemon. The
`agent/claude` task creates a session-specific socket path and, when
`channel-server` is available, writes Claude MCP configuration that launches
`channel-server` with `CHANNEL_SOCKET_PATH` set. Claude starts it as an MCP
subprocess.

Three producers connect to a channel-server socket:

- the plect `unix_socket` event channel shipped by `agent/claude`, which sends
  a `message` envelope and does not register or receive replies;
- `slack-adapter`, which registers a Slack thread and sends Slack inbound
  messages as `message` envelopes;
- `channel-server send`, a manual utility producer.

The socket protocol is a 4-byte big-endian length prefix followed by a JSON
envelope from `contracts/channel-protocol`. The active message types are
`register`, `message`, `reply`, and `permission`.

When channel-server is down, consumers degrade differently. The plect
`unix_socket` channel retries briefly and records `plect.channel.error`.
`slack-adapter` rejects subscribe requests when it cannot connect. Existing
adapter connections are dropped when a channel-server exits, and permission
nonces held in channel-server memory are lost on restart.

The adoption boundary is the problem: enabling a plugin should make its
plugin-owned daemons known to Plecture. The host should own only the
`plect bus serve` unit.

## Decision

Plecture uses first-class plugin service declarations. `plugin.toml` supports
`[[services]]` entries for bus-supervised plugin daemons. The bus supervisor
starts declared services when the bus starts, restarts crashed children with
bounded backoff, records service status, restarts services when their plugin
content changes, and stops services when the bus stops.

Service declarations are provider-agnostic. A declaration names a
plugin-owned executable, arguments, environment/config bindings, readiness or
health checks, restart policy, and log policy. Catalog content does not carry
secrets; Slack tokens, GitHub credentials, and similar values stay in user
configuration, environment, or environment files.

Plecture introduces no plugin dependency mechanism. Plugin boundaries are drawn
so runtime contracts do not cross them. When an apparent dependency edge appears,
that is a boundary smell, and the first remedy is merging the plugins that share
the runtime contract.

The planned regrouping merges `agent/runtime`, `agent/claude`, `agent/codex`,
`channel-server`, and `channel/slack` into one self-contained session-runtime
plugin. The final name can be refined during implementation. The tmux plumbing
is genuinely shared runtime logic with no agent-specific branch, so it belongs
inside the merged plugin. `channel-server` and `slack-adapter` become
`[[services]]` in that plugin. Mounted-but-unused definitions cost nothing, and
the Slack service stays naturally inert without credentials.

`github` remains self-contained and declares the `github-watcher` service.
`okf` remains self-contained because its provider, resource, task, workflow, and
template pack are one OKF contract surface.

`plect-agent-activity` is de-generalized. The session-runtime plugin carries
separate branch-free activity scripts for Claude and Codex. The previous shared
shape branched on `claude` versus `codex`; that branch was forced
generalization. Incidental duplication of small glue is accepted under the
write-it-twice rule.

## Consequences

Only the `plect bus serve` unit remains host-owned. Plugin daemons become part
of the mounted plugin surface and are supervised by the bus.

The `github` plugin declares a `github-watcher` service. The session-runtime
plugin declares `channel-server` and `slack-adapter` services, while per-session
channel-server instances can continue to be launched by Claude Code through MCP
configuration until the implementation moves that lifecycle under the bus.

Service state is bus-global and includes service identity, running state, pid,
restart count, last exit, last error, last health result, plugin id, and the
plugin lock coordinate or content hash that produced the running process.

Core needs manifest parsing and validation for `[[services]]`, a bus-owned
service supervisor, a service status model, and plugin update/remove signaling
or lockfile polling. It does not need plugin dependency parsing, dependency
closure checks, capability matching, or version solving.

Plugin follow-up work includes regrouping the session-runtime plugin, moving
`channel-server` and `slack-adapter` into that plugin, declaring
`github-watcher`, `channel-server`, and `slack-adapter` services, splitting
`plect-agent-activity` into branch-free per-agent scripts, and restructuring
the shipped plugins to the `config/` layout described by the design doc.

## Alternatives considered

### Bus-scoped supervised tasks

Extending tasks with `scope = "bus"` would reuse familiar setup/cleanup syntax,
`{{bin}}` resolution, and parts of the health vocabulary. It loses because task
machinery is session- and workflow-scoped: task rendering assumes session
variables, workdirs, workflow/environment outputs, node inputs, and persisted
session task state. Bus scope would still require a new process supervisor,
bus-global state, restart backoff, log handling, secret/config injection,
plugin update handling, and service coordination.

### Host-owned service units

Host-owned systemd, launchd, or other service units are the baseline. They are
rejected because enabling a plugin should be sufficient to make its daemons
known to Plecture.

### Channel-server as its own plugin

Making channel-server its own plugin isolated the Claude-channel protocol
implementation, but it also forced a runtime contract across plugin boundaries.
The final boundary rule takes the same observation to its conclusion: the
session runtime surface that needs channel-server, Slack delivery, agent tasks,
and agent channels belongs in one plugin.

### Sharing agent activity through `requiredBin`

A narrow `requiredBin` helper would let agent plugins reference a shared
activity executable from `agent/runtime`, but the activity helper no longer
triggers that mechanism once it is split into branch-free per-agent scripts.
Two small copies are cheaper than introducing cross-plugin executable
references for two consumers.

`requiredBin` is deferred until a genuinely shared, non-splittable helper with
three or more dependents appears.

### Identity-based plugin `requires`

An identity-based `requires` field would let one plugin name another plugin
directly. It loses because identity edges hide the actual contract being
consumed and block provider substitution. OSGi's `Require-Bundle` history is the
warning: bundle-identity dependencies were deprecated in favor of
contract-based `Import-Package` and `Require-Capability` because consumers
should depend on what they need, not on who happens to provide it. It also loses
because the regrouping leaves nothing to require across plugin boundaries.

### Capability `provides` and `uses`

Declaring capabilities in `plugin.toml` would make non-file protocol contracts
visible, but it would be a paper promise unless the protocol itself verifies
compatibility. Real channel-server compatibility belongs in an in-band
handshake, not in a catalog-side declaration that can drift.

### Derived file-backed contracts with signature depth

Deriving consumed task, channel, and template ids from shipped config would
produce better missing-provider errors, but it is machinery guarding no boundary
that should exist after regrouping. If a future split becomes necessary, static
derivation is the natural lint-style tool for seeing what crosses the boundary.

### Zero-declaration Unix framing

The zero-declaration framing is endorsed. The practical Unix answer is that
small cooperating pieces can live in the same distributable plugin when they
share one runtime contract. The final decision applies that framing by merging
the session runtime pieces instead of inventing declarations for them.

### Dependency version constraints

Catalog-snapshot-family tools use name-only dependencies or exact source paths:
Homebrew `depends_on`, Scoop dependencies, Nix exact paths, and VS Code
`extensionDependencies`. VS Code also pairs name-only inter-plugin dependencies
with a host minimum-version gate. That precedent remains useful if a future
split introduces real cross-plugin dependencies, but it is moot in this design
because no dependency edge remains.

Range constraints and solvers belong to independently versioned registry
ecosystems such as Helm, apt, Terraform, npm, and Go MVS. Plecture's catalog
plugins are locked by Git SHA or editable path. Related plugins can change
atomically in one catalog PR, and contract tests should catch real executable
or config drift. No constraint mechanism ships until those safeguards prove
insufficient.

Version-in-name capabilities, revision-skew warnings, closure-update support,
and same-revision enforcement are moot without cross-plugin dependency edges.
They remain reference material for a future split. Version solving remains a
non-goal: Git SHAs are unordered, and ordering would require semver discipline
for catalog plugins.

Dependency machinery in any form is deferred until there is a proven need to
split a plugin such that a runtime contract must cross the boundary. At that
point this ADR should be superseded, and this alternatives tour is the starting
material.

Tag-based release channels are deferred as an operational note for when a
second user arrives. There is nothing to build now.
