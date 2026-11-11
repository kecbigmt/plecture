---
superseded_by: 2026-11-11-service-executable-action-helpers
supersedes:
  - 2026-08-16-plugin-service-lifecycle
  - 2026-08-17-plugin-boundary-contracts
---

# Operator-declared plugin services and core subscriptions

## Context

Plugin-owned `[[services]]` declarations start resident processes when a plugin
is enabled. The supervisor passes its environment to every child, exposing
unrelated credentials. A plugin update can replace running service code even
though the operator never declared that service. Slack and GitHub plugins also
keep separate subscription registries, sweep logic, and event fan-out. Core
already manages node subscriptions and subscription retries, so these stores
answer the same lifecycle question in different places.

A deployment can run one dispatcher for mentions of a bot and create review
sessions for matching GitHub pull requests. It can use several Slack bots.
The subscription and delivery contract must serve these without making a
service's private state an authority over session membership.

### Local transport threat model

Browser content on the same host and other OS users must not reach a service's
local control surface or credentials. Code running as the same OS user as
plect is outside this boundary: it can read that user's files and process
environments, and containing it requires a separate user or sandbox. Services
have no local listener; their only control channel is private stdio with the
plect process that supervises them. Service env files are owner-readable only;
validated `[services.config]` contains no secrets and reaches the service as
the first stdio message. No group- or world-readable credential file is an
acceptable transport.

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

### Service declarations and configuration

Plugins mark resident-capable executables with `service = true` in
`[[executables]]`. The marker is metadata and starts nothing. Plugin-owned
`[[services]]` and `required_env` are removed; a manifest containing the old
service table fails loading with a migration-doc pointer. The plugin manifest
schema advances to version 3; the catalog manifest remains version 2.

The operator declares services in `<config-home>/services/*.toml`. Each
`[[services]]` entry has a unique `name`, a fully qualified `executable`
marked `service = true`, an `env_file`, and optional `args`, `restart`,
`health`, and non-secret `[services.config]`. A service name starts with a
lowercase ASCII letter, contains only lowercase letters, digits, and hyphens,
and is at most 64 characters. Names are unique across service files. Two
services naming the same executable are a load error. One Slack service holds
several bot declarations instead of running an adapter process per bot.

`env_file` is an absolute path to an owner-readable file of `KEY=VALUE` lines.
Blank and `#` comment lines are ignored; duplicate or invalid keys, malformed
lines, relative paths, unreadable files, interpolation, and a key conflicting with plect-provided `PLECT_DATA_DIR`
or `PLECT_CACHE_DIR` fail loading. A service receives these entries plus
`PLECT_DATA_DIR` and `PLECT_CACHE_DIR`; it does not inherit the supervisor
environment. The executable validates its own required variables. `restart` defaults to `on-failure` and
may be `never`; `health` defaults to `{ type = "process" }`, the only health
kind here. An explicit `plect plugin update` authorizes restarting an already
declared service when its code changes. Enabling or updating a plugin creates
no service declaration.

A service executable carries its JSON Schema inline as `config_schema` in its
`plugin.toml` executable entry. Core validates `[services.config]` against it
and treats the validated value as opaque. Plugin authors keep secret values
out of that schema; token values belong in `env_file`. For example:

```toml
schema_version = 3
version = "0.1.0"
plect_min_version = "0.0.0"
description = "Slack service and resource support."

[[executables]]
name = "slack-adapter"
path = "bin/slack-adapter"
service = true

[executables.config_schema]
type = "object"
required = ["bots"]
additionalProperties = false

[executables.config_schema.properties.bots]
type = "array"
minItems = 1

[executables.config_schema.properties.bots.items]
type = "object"
required = ["app_token_env", "bot_token_env", "channel_id", "allowed_user_ids"]
additionalProperties = false

[executables.config_schema.properties.bots.items.properties]
app_token_env = { type = "string" }
bot_token_env = { type = "string" }
channel_id = { type = "string" }
allowed_user_ids = { type = "array", items = { type = "string" } }
```

The operator declares one adapter for two bots. Environment variable names in
`[services.config]` identify keys in the service's private `env_file`; their
values do not appear in user configuration:

```toml
[[services]]
name = "slack"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack.env"

[services.config]
[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_PRIMARY"
bot_token_env = "SLACK_BOT_TOKEN_PRIMARY"
channel_id = "C0123"
allowed_user_ids = ["U0123"]

[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_SECONDARY"
bot_token_env = "SLACK_BOT_TOKEN_SECONDARY"
channel_id = "C0456"
allowed_user_ids = ["U0456"]
```

The adapter rejects duplicate `channel_id` values across its bots, so an
outbound call selects one bot from the thread's channel. Plect supplies
`PLECT_DATA_DIR` to the supervised service process only, set to the mode-`0700`
`<data-home>/plugins/<plugin>/` directory. The data home resolves from
`--data-home`, then `PLECT_DATA_HOME`, then `$XDG_DATA_HOME/plect`, then
`~/.local/share/plect`. The directory holds authoritative service state such
as GitHub per-resource last-seen state and Slack once-per-thread denial
records. Actions never receive it. The service needs this directory because it
does not inherit the supervisor's other environment variables. Plect does not
rewrite any `XDG_*` values supplied by the service's env file. It receives no global
data- or config-home path and never invokes the plect CLI. Plugins receive
neither `PLECT_CONFIG_HOME`, `PLECT_DATA_HOME`, nor `PLECT_CACHE_HOME`; their
only plect-provided environment bindings are the plugin data directory
(`PLECT_DATA_DIR`) for services and the plugin cache directory
(`PLECT_CACHE_DIR`) for all executable invocations.

Every invocation of a plugin executable, including a supervised service or a
node, channel, or hook action such as `observe`, receives `PLECT_CACHE_DIR` set
to `<cache-home>/plugins/<plugin>/`. Plect creates that plugin-scoped directory
with mode `0700`. The cache home resolves from `--cache-home`, then
`PLECT_CACHE_HOME`, then `$XDG_CACHE_HOME/plect`, then `~/.cache/plect`.
Catalog snapshots live under `<cache-home>/catalogs/`. The plugin cache holds
only disposable data such as tokens, ETags, and rate backoff; correctness cannot
depend on it. Plect never reads its contents and may delete it wholesale on a
plugin update or cleanup. Plugins own their cache locking. Core subscription
membership and the service's authoritative per-resource watch state are never
shared through the cache. The GitHub watcher and standalone `observe` share an
App token cache and rate budget there.

The first message plect writes to a service's stdin is
`{"op":"config","config":{...}}`, carrying the validated non-secret
`[services.config]`. No other traffic precedes it. The service writes
JSON-lines requests to stdout, including
`{"op":"publish","id":"request-1","resource":...,"type":...,"summary":...,"body":...,"metadata":...}`.
Every request carries an `id`. Its supervising plect process writes replies
to the service's stdin with the same `id`: a result such as
`{"op":"published","id":"request-1","delivered":N}`, or
`{"op":"error","id":"request-1","code":...,"message":...}`. Notifications
carry no `id`: `{"op":"subscriptions","resource_type":...,"add":[...],"remove":[...]}`
gives a full subscription snapshot when the service starts and deltas
thereafter on the same ordered stream. The service writes logs to stderr.
Nothing calls into the service;
its provider connection is separate from this local control channel.

An executable marked `service = true` receives its declaration's `env_file`
entries on *every* invocation, including node, channel, and hook actions.
The supervised process receives its validated config through the first stdio
message. An action receives config only when its definition explicitly refers
to `service.config` or a sub-path in an argument, stdin value, or shell binding,
using the ordinary value mechanism. No config environment variable is set.
An action invocation keeps its ordinary action environment, then overlays the
service's env-file values; the service values win on duplicate keys. An action
invoking a service-marked executable without a declaration fails with an
error naming the executable. Since one executable can name only one service,
the binding is unambiguous. Slack thread creation, posts, status, and
streaming call the provider Web API directly using these credentials;
workflow `base_url` inputs are removed.

### Core subscription and delivery contract

Core owns a `subscriptions` table keyed by
`(session_id, resource_id, source)`, with `source` equal to `runtime`,
`dispatch`, `task`, or `node`, plus `inputs` JSON and `created_at`. Each source
records a separate reason for one
session-resource binding:

- `plect subscribe <resource-id>` writes the calling session's `runtime` row.
  A repeated subscribe for that source is idempotent.
- `plect up <url>` writes a `dispatch` row for the session's entry resource,
  including when a chain or query population brings up that session. The row
  remains through down/up and is removed on session destroy.
- Binding a task instance to a resource, including through `plect up
  --task-resource` or `plect task setup --resource`, writes a `task` row. It
  remains while any task instance in that session binds the resource and is
  removed when the last such task is cleaned up. Every write that changes
  task-resource bindings reconciles the row with the stored task instances
  in the same database transaction, including admission and cleanup.
- A node with `subscription` writes a `node` row when it starts. The row stays
  while any live node in that session binds the same resource and is removed
  when the last such node ends.

`plect unsubscribe <id>` removes only the calling session's `runtime` row for
resource `<id>`; it cannot remove a `dispatch`, `task`, or `node` reason.
Destroying a session removes all its rows while retaining its tombstone, and deleting the
session row also cascades.
`plect subscription list [--session] [--resource-type] --json` exposes the
rows to operators; services receive
the same membership as a startup snapshot and later stdio deltas. A resource
may set `max_subscribers`; the limit counts distinct sessions per resource,
not rows, so another source for an already-subscribed session does not use a
second slot. Resource `subscribe` and `unsubscribe` hooks and
`subscription_retries` are removed; `observe` remains. The transaction
replaces the delivery lock and retry queue used for task binding and cleanup
decisions. Other source rows keep delivery active when a task is cleaned up.

`plect event publish --resource <id>` and a service's stdio `publish` request
append the event once per distinct `(session_id, resource_id)` subscription
and report the delivered session count. A failed append is reported, not
treated as zero delivery. Core is provider-neutral: it stores resource
identifiers and session references, then fans out events.

The Slack bot user is a resource named `slack://<team_id>/<bot_user_id>`. Its
`max_subscribers` is 1. Bringing up a session with this entry resource writes
its `dispatch` row; a session with another entry resource can use
`plect subscribe slack://T0123/U0123` to write a `runtime` row. Both persist
while the session is down. Mentions use `user.emit`, so no channel `include`
change or workflow-level `subscription` field is needed. These resource
definitions show the relevant contract; their `observe` actions retain the
ordinary resource
status surface:

```toml
[slack_bot_user]
kind = "resource"
match = '^slack://(?P<team_id>[A-Z0-9]+)/(?P<bot_user_id>[A-Z0-9]+)$'
name = { expr = "'slack-bot/' + match.team_id + '-' + match.bot_user_id" }
max_subscribers = 1

[slack_bot_user.observe]
type = "exec"
bin = "slack-adapter"
args = ["resource", "observe", "--resource", { from = "resource.id" }]
```

```toml
[slack_thread]
kind = "resource"
match = '^https://[A-Za-z0-9.-]+\.slack\.com/archives/(?P<channel_id>[A-Z0-9]+)/p(?P<ts16>\d{16})$'
name = { expr = "'slack/' + match.channel_id + '-' + match.ts16" }

[slack_thread.observe]
type = "exec"
bin = "slack-adapter"
args = ["resource", "observe", "--resource", { from = "resource.id" }]
```

For a message that passes that bot's user and channel allow check, the adapter
publishes `user.emit` to the thread resource through stdio. A positive
delivered count ends routing. On zero delivery it publishes to the bot-user
resource; zero delivery there drops the mention with a log line naming the
channel, thread, and reason. Denial replies come from the adapter before
routing. There is no population routing step.

### Workflows and populations

An orchestrator may use the bot user as its entry resource; a separately
created worker may use a Slack thread. Bringing either up writes its
`dispatch` subscription:

```toml
[orchestrator]
kind = "workflow"
resource = "slack_bot_user"

[thread_worker]
kind = "workflow"
resource = "slack_thread"
```

Populations retain their query `uses` (`poll` or `subscribe`) and `[query]`
contract. A GitHub poll population still creates review sessions for labeled
pull requests:

```toml
[pull_review]
kind = "workflow"
resource = "pull_request"

[[pull_review.populations]]
name = "labeled_pulls"
uses = ["poll"]
poll_every = "5m"

[pull_review.populations.query]
repositories = ["example/project"]
labels = ["agent-review"]
state = "open"
draft = false
```

The Slack thread resource's `query.subscribe` and the adapter's
`/unbound-mentions` and `subscribe unbound-mentions` paths are removed.
Creating a session for every new mention thread is deferred until a concrete
use case calls for it. The GitHub watcher learns subscription membership from
the stdio startup snapshot and deltas and keeps per-resource watch state in
`PLECT_DATA_DIR`; it no longer owns `subscriptions.json`.
Standalone GitHub `observe` remains callable without a watcher service and
uses its own credentials. Both invocations share disposable App token and rate
budget caches through `PLECT_CACHE_DIR`.

### Service listing and scope

`plect service list` shows each declared service's name, executable, state,
pid, and restart count. `--all` also shows one `available` row for each
service-marked executable of an enabled plugin with no declaration. With one
Slack service and an unused GitHub watcher:

```text
NAME   EXECUTABLE                         STATE      PID    RESTARTS
slack  official/slack/slack-adapter       running    4102   0
       official/github/github-watcher     available
```

`channel-server` remains a per-session MCP subprocess launched by Claude
Code. It has no resident service declaration.

## Consequences

The implementation has one stage: the core subscription table, transactional
task binding and cleanup, source-scoped rows and deduplicated delivery,
resource-addressed publish, `max_subscribers`, the stdio service protocol,
service-config passing, plugin-scoped authoritative data and disposable cache
directories, the one-process Slack adapter, and the bot-user resource arrive
together with removal of the Slack
unbound-mention feed. Populations retain their existing contract.

The implementation removes plugin-owned service declarations, resource
subscription hooks, the delivery lock and retry queue, Slack subscriber and
instance registries, the #742 `--state-dir` split and bot-scoped `mentions`
resource, and GitHub
`subscriptions.json`. It adds schema and conformance fixtures and behavior
tests for overlapping task, dispatch, runtime, and node sources, concurrent
task setup and cleanup, subscriber limits, persistence, delivery once per
session/resource, stdio snapshots and deltas, service config on every
invocation, cache isolation and reuse, request/reply correlation and errors,
and failure paths. The breaking change ships with a backup-first procedure in
`docs/migrations/`. Its one-time migration maps existing GitHub watcher
`subscriptions.json` rows, Slack adapter `subscribers.json` rows,
`subscription_retries`, and `pending_delivery.json` into the new subscription
table, or drops a row with a logged reason. It ships in the same implementation
PR; there is no compatibility shim.

`PLECT_CACHE_HOME` and `--cache-home` name plect's cache root, with catalog
snapshots under its `catalogs/` child and disposable plugin caches under
`plugins/<plugin>/`. They formerly named the catalog cache directory itself.
Operators setting either override point it one level higher; the backup-first
procedure is in [the cache-home migration](../migrations/plugin-cache-home-root.md).
No compatibility path interprets the old value.

## Alternatives considered

### Separate Slack adapter processes per bot

Separate processes do not isolate secrets from code running under the same OS
user. One service configuration with multiple bot entries removes duplicated
process and registry state.

### Service-owned subscription registries

Separate registries in Slack and GitHub require each plugin to maintain its
own membership, cleanup, and fan-out while core already manages session
lifecycle. The core table supplies one durable authority.

### Resident channel-server

The channel-server connection and permission nonce state belongs to one
Claude Code session. A resident instance would need a separate multiplexing
contract and would not reproduce that session's MCP lifecycle.
