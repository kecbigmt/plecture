---
supersedes:
  - 2026-08-16-plugin-service-lifecycle
  - 2026-08-17-plugin-boundary-contracts
---

# Operator-declared plugin services

## Context

The service lifecycle decision made enabling a plugin sufficient to start its
resident processes. A plugin manifest can declare only one instance of each
service. The supervisor also gives every child its own environment, so a Slack
adapter can receive GitHub credentials and two Slack adapters can receive each
other's credentials. A plugin update can replace a resident process without a
separate operator decision to run that process.

The Slack adapter supports multiple instances at the executable level and
publishes a bot-specific address in its instance registry. The `mentions` hook
uses that registry, but the local HTTP endpoint has no authentication. The
resident service boundary therefore needs both an operator-owned instance
declaration and a protected local API.

## Supersession scope

This decision replaces these decisions of
`2026-08-16-plugin-service-lifecycle`:

- `## Decision`: `plugin.toml` service declarations and start on plugin enablement.
- `## Decision`: plugin-provided service environment and `required_env` gating.
- `## Consequences`: the service identity and CLI surface derived from plugin service declarations.

It replaces these decisions of `2026-08-17-plugin-boundary-contracts`:

- `## Decision`: retained plugin-owned `[[services]]` declarations.
- `## Consequences`: manifest parsing and validation for plugin-owned `[[services]]`.

## Decision

Plugins mark resident-capable executables with `service = true` in
`[[executables]]`. The marker is metadata only. A `plugin.toml` containing
`[[services]]` fails to load, with an error pointing to the service migration
procedure. The plugin manifest format advances to schema version 3 to make
the break explicit; the catalog manifest format remains version 2. Enabling
or updating a plugin does not create a new service.

The operator declares instances in user-owned `<config-home>/services/*.toml`.
Each `[[services]]` table has a unique operator-chosen `name`, a fully
qualified `executable` (`<catalog>/<plugin>/<executable>`), optional `args`,
and an `env_file`. The executable must exist in an enabled plugin and
carry `service = true`, or configuration loading fails. A file may contain
multiple service tables. Names are unique across the entire user config,
including separate files; they use lowercase ASCII letters, digits, and
hyphens, start with a letter, and are at most 64 characters. A declaration
names an instance, so several names may use the same executable.

`env_file` is an absolute path to an operator-owned file of `KEY=VALUE` lines.
Blank lines and lines beginning with `#` are ignored. Duplicate keys, invalid
names, unreadable files, and malformed lines fail configuration loading. The
file must be readable only by its owner. Relative paths and environment
interpolation are rejected. A service receives only these entries and the
runtime values plect supplies for its own APIs and instance paths. It does not
inherit the supervisor environment. Operators put any required `PATH`, `HOME`,
credential, or provider-specific setting in that service's file. The executable
validates its own required inputs; there is no `required_env` gate.

`restart` defaults to `on-failure`, with `never` available. `health` defaults
to `{ type = "process" }`; that is the only health kind in this decision.
`args` defaults to an empty list. The operator can override the two policies
per service. The resident process supervises declared instances and records
their state, pid, restart count, and last error. It restarts a declared instance
when its plugin content changes. Plugin removal makes a referencing service
invalid at the next config load; it does not silently switch executable.

For one Slack App, the user file contains one block:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-primary.env"
```

For two Apps, the file contains two blocks that reference the same executable
but use separate credentials and state:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-primary.env"

[[services]]
name = "slack-secondary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-secondary.env"
```

Each service gets a private state directory derived from `name`, under
`<data-home>/services/<name>/`, and a Unix socket path `api.sock` in
that directory. plect creates the directory with mode `0700` and passes
`PLECT_SERVICE_STATE_DIR` and `PLECT_SERVICE_SOCKET` to the child. It also
passes `PLECT_BUS_SOCKET` and, when needed, `PLECT_BUS_TOKEN` for the local
event bus. A service's own `env_file` can set its adapter-specific config and
secrets but cannot override these `PLECT_*` runtime values. If the resolved
socket path exceeds the platform's Unix socket limit, loading fails with an
actionable path error. The service name is the stable state identity; renaming
it starts a distinct instance with distinct state.

Local APIs used by hooks, including the Slack adapter API, listen on that Unix
socket. The parent directory is private and the socket is accessible only to
its owner. The Slack adapter's bot-specific instance registry publishes the
socket path after the listener is ready; the `mentions` hook dials the socket
identified by team and bot, retaining the existing bot-identity check. Thread
hooks also use a configured socket path. Missing or stale registry entries fail
the hook for retry; they do not route it to an unprotected TCP fallback. A
provider's public ingress, such as a webhook receiver, keeps its own external
authentication and is not this local hook API.

`plect service list` displays declared services with name, executable, state,
pid, and restarts. `--all` also displays one `available` row for each
service-marked executable in an enabled plugin that no declaration uses. Its
name and pid are empty. When several declarations use one executable, it has
several declared rows and no `available` row. There is no `plugin show`
addition in this decision.

With the two Slack services running and an enabled but unused GitHub watcher,
`plect service list --all` shows:

```text
NAME             EXECUTABLE                           STATE      PID    RESTARTS
slack-primary    official/slack/slack-adapter         running    4102   0
slack-secondary  official/slack/slack-adapter         running    4103   1
                 official/github/github-watcher       available
```

`channel-server` remains a per-session MCP subprocess launched by Claude Code.
It is not marked as a resident service and has no operator service declaration.
This preserves its session-specific socket and lifecycle.

## Consequences

Plugin installation makes resident-capable programs available without running
them. Every running service has an explicit user-owned declaration and a
separate environment. The supervisor keeps one host-owned resident process,
`plect serve`, while service identity becomes the operator-chosen name.

The implementation removes manifest `[[services]]` and `required_env`, adds
user service config loading, and updates the supervisor and CLI. The breaking
change ships with a `docs/migrations/` procedure that begins with a backup and
gives one user-config block reproducing each resident shipped service:
`slack-adapter` and `github-watcher`. Its `channel-server` section explains
that Claude Code already starts it per session and that no resident declaration
is needed. There is no compatibility shim.

## Alternatives considered

### Per-service shared secrets for local HTTP

HTTP bearer tokens would preserve the adapter's TCP transport, but each hook
needs a reliable way to retrieve the right instance's token. Publishing tokens
in the instance registry duplicates secret material on disk; pointing hooks
back at service env files couples them to operator credential storage. Unix
socket permissions already give same-user hooks the necessary local access,
while registry records can contain only a path.

### Deriving a TCP port from the service name

A name cannot reliably map to a free port. A deterministic private socket path
avoids collisions and requires no port allocation or shared listener.

### Resident channel-server

The channel-server connection and permission nonce state belongs to one Claude
Code session. A resident instance would require a different multiplexing
contract and would not reproduce the session-specific MCP lifecycle.
