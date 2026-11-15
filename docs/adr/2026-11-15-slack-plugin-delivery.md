# Slack plugin delivery through core subscriptions

## Context

The Slack adapter keeps a subscriber registry and an unbound-mention feed,
while plect already owns session lifecycle and resource subscriptions. An
adapter process per bot duplicates process and registry state without isolating
secrets from code running as the same OS user. The core service and
subscription contract in [operator-declared plugin services](2026-11-09-operator-declared-plugin-services.md)
provides one service declaration, resource-addressed publishing, and a
subscription snapshot followed by deltas over stdio.

A deployment may run a standing session for a bot while other sessions follow
individual threads. An allowed mention must reach a thread subscriber first
and a bot subscriber only when no thread subscriber receives it.

## Decision

![One adapter service checks each mention, publishes it to the thread resource, falls back to the bot-user resource when no thread subscriber received it, and otherwise drops it with a log line. An unbound-mention feed with per-thread session creation and a separate adapter process per bot are rejected.](assets/2026-11-15-slack-plugin-delivery-mention-routing.svg)

### One adapter and outbound calls

One declared `slack-adapter` service holds multiple bot declarations in its
validated `[services.config]`. Each bot names its App and bot token environment
variables, channel, and allowed users; token values live in the service's
private `env_file`. The adapter rejects duplicate `channel_id` values across
bots, so an outbound call selects one bot from the thread's channel. Thread
creation, posts, status, and streaming call the Slack Web API directly using
these credentials. Workflow `base_url` inputs are removed.

The service receives its configuration as its first stdio message. Action
invocations of the service executable use explicit `service.config` value
references when they need configuration. Their environment receives the
service's env-file values under the core contract.

### Bot and thread resources

The bot user is a resource named `slack://<team_id>/<bot_user_id>` with
`max_subscribers = 1`. An orchestrator can use it as its entry resource,
creating a `dispatch` subscription. A session with a different entry resource
can subscribe to `slack://T0123/U0123` at runtime. Both subscriptions survive
a down/up cycle. A worker may use a Slack thread as its entry resource.
Mentions use `user.emit`; neither channel `include` nor a workflow-level
`subscription` field changes.

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

```toml
[orchestrator]
kind = "workflow"
resource = "slack_bot_user"

[thread_worker]
kind = "workflow"
resource = "slack_thread"
```

The resources' `observe` actions retain the ordinary resource status surface.

### Mention routing

The adapter performs each bot's user and channel allow check and sends denial
replies before routing. For an allowed mention, it publishes `user.emit` to
the thread resource through stdio. A positive delivered count ends routing.
On zero delivery, it publishes to the bot-user resource. Zero delivery there
drops the mention with a log line naming the channel, thread, and reason.
There is no population routing step.

The Slack thread resource's `query.subscribe` and the adapter's
`/unbound-mentions` and `subscribe unbound-mentions` paths are removed.
Creating a session for every new mention thread is deferred until a concrete
use case calls for it. Other populations retain their query contract.

### Other plugin boundaries in this stage

The GitHub watcher receives subscription membership from the core stdio
startup snapshot and deltas. It keeps per-resource watch state in
`PLECT_DATA_DIR` and no longer owns a subscription ledger. Standalone GitHub
`observe` remains callable without a watcher service and uses its own
credentials. The watcher and standalone observation share disposable App token
and rate budget caches through `PLECT_CACHE_DIR`.

The Claude Code `channel-server` remains a per-session MCP subprocess, with no
resident service declaration.

## Consequences

The implementation removes the Slack subscriber and instance registries, the
multi-instance `--state-dir` split, the bot-scoped `mentions` resource, and the
unbound-mention feed. One adapter process serves the configured bots. Its
once-per-thread denial records are authoritative service state in
`PLECT_DATA_DIR`. The GitHub watcher's per-resource watch positions are another
example of authoritative service state; action invocations do not receive
that directory. The migration of plugin-owned subscription records to core is
specified by the backup-first procedure shipped with the implementation.

## Alternatives considered

### Separate Slack adapter processes per bot

Separate processes do not isolate secrets from code running under the same OS
user. One service configuration with multiple bot entries removes duplicated
process and registry state.

### Keep a population feed for unbound mentions

A new mention thread has no current per-thread session creation use case in
the target deployment. The bot-user resource supplies the standing recipient;
a feed with its own registration and replay state would duplicate lifecycle
work before a concrete consumer needs it.

### Resident channel-server

Its connection and permission nonce state belongs to one Claude Code session.
A resident instance would need a separate multiplexing contract and would not
reproduce that session's MCP lifecycle.
