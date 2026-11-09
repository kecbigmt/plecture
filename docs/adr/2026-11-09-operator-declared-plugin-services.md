---
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

A deployment can run one dispatcher for mentions of a bot, create a worker
session for each new thread, and create review sessions for matching GitHub
pull requests. It can use several Slack bots. The subscription and delivery
contract must serve all of these without making a service's private state an
authority over session membership.

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
lines, relative paths, unreadable files, interpolation, and a conflicting
`PLECT_*` runtime key fail loading. A service receives these entries and
plect-provided runtime values, not the supervisor environment. The executable
validates its own required variables. `restart` defaults to `on-failure` and
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

The service keeps its API cache, rate budget, and per-resource watch state in
`<data-home>/services/<name>/`, a private mode-`0700` directory supplied as
`PLECT_SERVICE_STATE_DIR`. No service exposes a local API for hooks or
actions; the service calls `plect` for subscription lookup and event
publication. Its inbound provider connection is separate from this contract.
An executable marked `service = true` receives the selected
service's `env_file` entries and the validated service config on *every*
invocation, including node, channel, and hook actions. Plect writes that
config as JSON to a private file and passes its path in
`PLECT_SERVICE_CONFIG`. Since one executable can name only one service, this
binding is unambiguous. Slack thread creation, posts, status, and streaming
call the provider Web API directly using these credentials; workflow
`base_url` inputs are removed.

### Core subscription and delivery contract

Core owns a `subscriptions` table keyed by `(session_id, resource_id)` with
`source` (`runtime`, `dispatch`, or `node`), `inputs` JSON, and `created_at`.
Destroying a session removes its subscriptions while retaining its tombstone;
rows also cascade if the session row is deleted. `plect subscribe <resource-id>`
records the calling session's subscription and runs no resource hook;
`plect unsubscribe <id>` removes that session's row for resource identifier
`<id>`. `plect subscription list [--session] [--resource-type] --json` exposes
the durable rows to services. The
subscription remains through session down/up. Resource `subscribe` and
`unsubscribe` hooks and `subscription_retries` are removed; resource
`observe` remains. A resource may set `max_subscribers`; admission beyond the
bound fails. Core is provider-neutral: it stores resource identifiers and
session references, then fans out events.

`plect event publish --resource <id>` appends an event to every subscriber's
log and reports the delivered count, including a member admitted by an
undelivered-publish population when there were no existing subscribers. The
publish operation uses the same durable subscription table as admission and
listing. A failed admission or append is reported, not treated as zero
delivery.

The Slack bot user is a resource named `slack://<team_id>/<bot_user_id>`. Its
`max_subscribers` is 1. A dispatcher session subscribes explicitly with
`plect subscribe slack://T0123/U0123`; the subscription persists even while
the session is down. Mentions use `user.emit`, so no channel `include` change
or workflow `subscription` field is needed. These resource definitions show
the relevant contract; their `observe` actions retain the ordinary resource
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

For a message that passes the adapter's user and channel allow check, the
adapter publishes to its thread resource. A thread subscriber or matching
undelivered-publish population makes the delivered count positive and ends
routing. On zero delivery, the adapter publishes to its bot-user resource. A
positive delivered count ends routing; zero delivery drops the message with a
log line naming the channel, thread, and reason. Denial replies come from the
adapter, before routing.

### Undelivered-publish populations

A population chooses exactly one source form: `resource_observer` plus
`[query]`, or `resource = "<resource type>"` plus `where = "<expression over
match.*>"`. Both forms together fail loading. The second form consumes a
resource-addressed publish that reaches zero subscribers and whose resource
match values satisfy `where`. In one core step, it creates the member session,
subscribes it to that resource, and delivers the triggering event. It does not
refetch history with `--catch-up-through`. The existing membership lifecycle,
including `expire_after`, tombstones, and `population_members`, applies. A
GitHub pull-request population using `query.poll` remains supported.

An orchestrator uses the bot resource; a worker uses a Slack thread. The
orchestrator's explicit bot subscription receives mentions only after no
thread subscriber or population takes them:

```toml
[orchestrator]
kind = "workflow"
resource = "slack_bot_user"

[thread_worker]
kind = "workflow"
resource = "slack_thread"

[[thread_worker.populations]]
name = "new_mentions"
resource = "slack_thread"
where = "match.channel_id == 'C0123'"
expire_after = "8h"
```

The operator brings up the orchestrator for `slack://T0123/U0123` and runs
`plect subscribe slack://T0123/U0123` in that session. The worker population
then creates a session for a new thread, stores its subscription, and appends
the initiating `user.emit` before any later thread mention. A subsequent
mention follows the existing thread subscription. An unmatched new thread
falls through to the bot resource. The adapter is not a population registry;
core owns admission, membership, and delivery.

GitHub watcher reads `plect subscription list --resource-type
<github-resource-type> --json` each cycle and keeps its API cache, rate
budget, and per-resource watch state privately. It no longer owns a
`subscriptions.json` file. Standalone GitHub `observe` remains callable
without a watcher service; it uses the action process's own credentials,
process-local rate budget, and App token cache. A poll population can still
use the GitHub resource query to create review sessions for labeled pull
requests, and the watcher uses core subscriptions to deliver updates.

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

The implementation has two stages. Stage 1 adds the core subscription table,
resource-addressed publish, `max_subscribers`, service-config passing, the
one-process Slack adapter, and the bot-user resource. The existing thread
population keeps its `/unbound-mentions` path during this stage only. Stage 2
adds the undelivered-publish population source, migrates the thread
population, and removes the old feed and `subscribe unbound-mentions` path.
Neither stage makes the transitional path a permanent compatibility API.

The implementation removes plugin-owned service declarations, resource
subscription hooks and retries, Slack subscriber and instance registries, and
GitHub `subscriptions.json`. It adds schema and conformance fixtures and
behavior tests for subscription admission, limits, persistence, fan-out,
zero-delivery population admission, service config on every invocation, and
failure paths. The breaking change ships with a backup-first procedure in
`docs/migrations/` and a one-time migration of existing subscription rows in
the same implementation PR; there is no compatibility shim.

Standalone GitHub `observe` can mint an App token on each invocation and
cannot remember rate backoff between processes. An observe-private cache
directory is the recommended follow-up for this remaining cost.

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
