---
supersedes:
  - 2026-08-16-plugin-service-lifecycle
  - 2026-08-17-plugin-boundary-contracts
---

# Operator-declared plugin services

## Context

The service lifecycle decision made enabling a plugin sufficient to start its
resident processes. A plugin manifest can declare only one instance of each
service. The supervisor also passes its environment to every child, so a Slack
adapter can receive GitHub credentials and two Slack adapters can receive each
other's credentials. Enabling a plugin can start a resident process; a later
explicit `plect plugin update` then replaces the code of that already-running
process without a separate service declaration.

The Slack adapter supports multiple instances at the executable level and
publishes a bot-specific address in its instance registry. The `mentions` hook
uses that registry, but the local HTTP endpoint has no authentication. The
resident service boundary therefore needs both an operator-owned instance
declaration and a protected local API.

### Local API threat model

The local API must reject requests from web content in a browser running on
the same host. Agents can direct a headless Chromium at arbitrary pages, and a
page can try DNS rebinding or a simple cross-origin POST to a loopback API.
Other OS users on the host must also be unable to call the API.

Code running as the same OS user as plect is outside this boundary. An agent
session with a shell or plugin code under that user can read service env files
and `/proc/<pid>/environ`, and can use any socket the user owns. Containing
such code requires a separate OS user or a sandbox, which is a separate
decision.

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
or updating a plugin does not create a new service. `plect plugin update` is
the operator action that authorizes replacing the code of an already declared
service; the supervisor restarts that instance on a content change.

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

plect supplies one `PLECT_RENDEZVOUS_DIR` rooted at
`<data-home>/rendezvous/` to every service and action process it starts,
including resource hooks and plugin executables. That value is computed by
plect from its resolved data home for all callers; neither an env file nor the
supervisor environment can override it. A conflicting `PLECT_*` runtime key in
an env file is a load error. plect creates the directory with mode `0700`.
Plugins choose subpaths for state shared between a resident process and its
hooks. Per-service private state stays under `PLECT_SERVICE_STATE_DIR`; the
rendezvous directory holds cross-process lookups and shared stores. It does not
choose an instance's private state.

The Slack adapter writes its bot-specific instance registry under
`<rendezvous-dir>/slack/instances/`. Its `mentions` hook reads that exact
registry, keyed by team and bot, and dials the recorded socket path. Registry
location never depends on either process's `HOME` or `XDG_STATE_HOME`. The
adapter uses `PLECT_SERVICE_STATE_DIR` for its subscriber and stream ledgers
when the operator sets no state path, independent of the shared registry.

The GitHub watcher's `serve`, `subscribe`, and `unsubscribe` commands use one
`subscriptions.json` under `<rendezvous-dir>/github-watcher/`, including when
the latter commands run as hooks. The watcher's `gh-api` helper and the
`github-issue-pr` observer use that same directory for their shared rate
budget and App token cache. These paths do not depend on separately resolved
`HOME` or `XDG_DATA_HOME` values. These are plugin-chosen subpaths; core knows
neither provider name. The implementation migrates existing shared stores
with a backup instead of creating an empty store while subscriptions remain.
The rendezvous directory conveys no credentials: hook-side GitHub auth is
configured independently of the watcher's service env file.

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
env_file = "/srv/plect/config/service-env/slack-primary.env"
```

For two Apps, the file contains two blocks that reference the same executable
but use separate credentials and state:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-primary.env"

[[services]]
name = "slack-secondary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-secondary.env"
```

Each service gets a private state directory derived from `name`, under
`<data-home>/services/<name>/`, and a Unix socket path `api.sock` in
that directory. plect creates the directory with mode `0700`; the adapter
creates the socket with owner-only mode `0600` before accepting connections.
plect passes `PLECT_SERVICE_STATE_DIR` and `PLECT_SERVICE_SOCKET` to the child.
It also passes `PLECT_BUS_SOCKET` and, when needed, `PLECT_BUS_TOKEN` for the local
event bus. A service's own `env_file` can set its adapter-specific config and
secrets but cannot override these `PLECT_*` runtime values. If the resolved
socket path exceeds the platform's Unix socket limit, loading fails with an
actionable path error. The service name is the stable state identity; renaming
it starts a distinct instance with distinct state. The 64-character name
limit is a syntax bound, not a promise that every name fits every data-home
path; the socket-path check applies to the resolved path.

Local APIs used by hooks, including the Slack adapter API, listen only on that
Unix socket. The browser's network APIs cannot dial a Unix socket, so DNS
rebinding and cross-origin POSTs have no TCP listener to reach. The directory
and socket modes exclude other OS users. No loopback proxy is supplied, since
one would reopen the browser path.

Every caller of the local API uses a Unix-socket client. The shipped Slack
`slack`, `stream`, and `status` channels remain `exec` channels invoking
`curl`: their configured `base_url` input becomes a `socket_path` input, and
their argv adds `--unix-socket <socket_path>` while the request URL becomes a
fixed `http://localhost/<endpoint>` transport placeholder. Slack's
`thread` and `mentions` resource hooks, the `slack_thread` task, and local
tools such as a `factory-reply`-style caller use the adapter's socket client
or `curl --unix-socket` with the selected path. The `thread` resource's
`base_url` query input becomes an absolute `socket_path` string. The
`slack_thread` task likewise takes an absolute `socket_path` input. These are
plugin-side config and client changes; no new core channel primitive is
introduced. All clients run as the service-owning OS user; a caller
under another user needs a separately designed mediation boundary, not wider
socket permissions. The Slack adapter's bot-specific instance registry
publishes the socket path after the listener is ready; the `mentions` hook
dials the socket identified by team and bot, retaining the existing
bot-identity check. The thread subscribe/unsubscribe hook reads the
operator-set `SLACK_ADAPTER_SOCKET_PATH` in the hook environment for its
default instance; an unset value fails the hook, and a user-owned resource
overlay can pass `--socket <path>` to select another socket. It never takes a
path from the service's private env file. Channels and tasks receive their
instance socket path from their configured inputs. Missing or stale registry
entries fail the hook for retry; they do not route it to an unprotected TCP
fallback. `github-watcher` has no inbound local
HTTP API in its bus-delivery mode; any future hook-facing API follows the same
socket rule. A provider's public ingress, such as a webhook receiver, keeps
its own external authentication and is not this local hook API.

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
user service config loading, updates the supervisor and CLI, and adds schema
and conformance fixtures for the user-service and manifest changes. The
breaking change ships with a `docs/migrations/` procedure that begins with a
backup and gives one user-config block reproducing each resident shipped
service:
`slack-adapter` and `github-watcher`. The Slack block needs neither `HOME` nor
`XDG_STATE_HOME` when its config and state paths are explicit; the shared
registry uses `PLECT_RENDEZVOUS_DIR`. The watcher block supplies `HOME` for
`gh` authentication and `XDG_CONFIG_HOME` if the operator uses a nondefault
`gh` config home. Its subscription, rate-budget, and token-cache paths use
`PLECT_RENDEZVOUS_DIR`, so `XDG_DATA_HOME` is not needed for those paths.
Hook-side GitHub authentication remains an explicit separate setup; the
service env file does not supply its credentials. The migration backs up and
moves the existing Slack registry and watcher shared data before starting the
new services. Its `channel-server` section
explains that Claude Code already starts it per session and needs no resident
declaration. There is no compatibility shim.

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
