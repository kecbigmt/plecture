# Slack plugin

The `slack-adapter` service holds one Socket Mode connection per configured
App. Plecture owns resource subscriptions and delivers events through the
service's private stdio protocol. The adapter uses the Slack Web API for
outbound actions.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/slack
```

Building `slack-adapter` requires a Go toolchain. Declare one supervised
service in `<config-home>/services/slack.toml`:

```toml
[[services]]
name = "slack"
executable = "official/slack/slack-adapter"
env_file = "/absolute/path/to/private/slack.env"
args = ["serve"]

[services.config]
socket_path = "/run/plect/slack-mentions.sock"
[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_PRIMARY"
bot_token_env = "SLACK_BOT_TOKEN_PRIMARY"
channel_ids = ["C0123", "C0456"]
allowed_user_ids = ["U0123"]
```

The owner-readable env file supplies the named tokens as `KEY=VALUE` lines.
The service requires at least one bot, rejects duplicate channel IDs across
Apps, duplicate App identities, and missing token bindings. It selects the
outbound App by channel ID. `socket_path` is required when a thread population
uses this service; the operator supplies the same path in that population's
query. An empty
`allowed_user_ids` allows everyone. Optional `denied_user_message` and
`denied_channel_message` send one reply per bot, channel, thread, and user;
empty values silently drop denied mentions.

## Resources and mentions

`config/resources/thread.toml` resolves a root or reply permalink to the same
thread session. The bot resource, `slack://<team_id>/<bot_user_id>`, permits one
distinct subscriber. A session can use either resource as its entry resource
or subscribe with `plect subscribe`.

For an allowed mention, the adapter publishes `user.emit` to the thread
resource. If the thread has no subscriber and a thread population covers that
channel, it persists the mention and emits an appearance to the population's
private local stream. Once the thread subscription appears, it publishes the
pending mention with the thread transcript. A disconnected stream does not
change coverage or send the mention to the bot subscriber. For an uncovered
allowed channel, the adapter publishes to the bot resource; zero delivery is
logged as a drop. Denied mentions are handled before publication.
Pending mentions are retained across restarts until delivery or a coverage
change routes them elsewhere; the service logs an unresolved mention and
repeats the warning at most once every five minutes per mention.

The population's `channel_ids` query is the routing authority. Two populations
may not cover the same channel for one App. Core sends the validated population
query snapshot before the adapter opens Socket Mode connections and refreshes
it on configuration reload. A subscribe-only workflow can declare:

```toml
[thread_worker]
kind = "workflow"
resource = "official.slack.thread"

[thread_worker.inputs_schema]
type = "object"
required = ["channel_id", "thread_ts"]
additionalProperties = false

[thread_worker.inputs_schema.properties]
channel_id = { type = "string" }
thread_ts = { type = "string" }

[[thread_worker.populations]]
name = "mentions"
uses = ["subscribe"]
expire_after = "1h"
auto_down = true
auto_destroy = true

[thread_worker.populations.query]
socket_path = "/run/plect/slack-mentions.sock"
channel_ids = ["C0123"]

[thread_worker.populations.session.inputs]
channel_id = { from = "item.channel_id" }
thread_ts = { from = "item.thread_ts" }
```

`expire_after`, `auto_down`, and `auto_destroy` use the ordinary population
rules. The workflow's `max_up_children` or the virtual-root cap bounds running
members. A crash after core accepts a publish but before the adapter records
success can replay the same mention once more; there is no cross-process
exactly-once guarantee.

## Restart recovery

On startup, the resident service scans each configured public channel with
`conversations.history`, then checks replies to recent roots and every thread
with an active subscription using `conversations.replies`. Recovered mentions
follow the same allow list, thread transcript, and routing as live mentions.
The service saves a versioned `recovery.json` in `PLECT_DATA_DIR`, keyed by
workspace, bot, and channel. It records the channel scan boundary, subscribed
thread boundaries, and processed message timestamps. Live and recovered
mentions share this record. The service logs the number of mentions recovered
on each restart.

The scan looks back at most 24 hours, with a five minute overlap around the
saved boundary. On first start without a state file, it begins at startup and
logs that earlier messages were not scanned. If downtime exceeds 24 hours, it
logs the exact omitted range. Failed channel scans retain the earlier channel
boundary for a later retry. An inaccessible subscribed thread retains its own
boundary while channel history continues. Both failures log the skipped range
and leave live delivery running.
The bot must be a member of each configured channel. Deleted or expired Slack
messages cannot be recovered. An unsubscribed thread whose root is older than
the lookback window is outside the scan. If the process crashes between core
accepting a publish and saving the message timestamp, replay can deliver it a
second time.

The shipped Slack App manifest already grants `channels:history` and
`app_mentions:read`; existing installations for configured public channels do
not need a new OAuth scope or reinstallation. The scan uses bounded pagination
and paces requests. Slack API rate limits can lengthen a large recovery.

The scan's value is measured by its per restart recovered mention count. If
hosts running it report no recovered mention by 2027-02-16, the owner decides
whether to remove it and keep the documented gap.

## Effects and channels

- `config/tasks/slack_thread.toml` creates a root message from `channel_id`
  and `root_text`. It returns `thread_ts`, `channel_id`, and `permalink`.
  A workflow subscribes to replies with
  `subscription = { from = "self.outputs.permalink" }` on that node.
- `config/tasks/thread_dir.toml` creates a directory for an existing thread,
  returning `workspace_dir` and the same thread identifiers. Its `dirs_root`
  input defaults to `$HOME/workspace_dirs`. A workflow can use `workspace_dir`
  as its `workdir`.
- `config/channels/slack.toml` posts an event's body or summary to a thread.
- `config/channels/status.toml` sets the thread's agent session status. Bind
  it to `plect.status_message` events. Nonempty events show Slack's working
  display; an empty event clears it when the turn ends.
- `config/channels/stream.toml` posts a complete `plect.message` event as one
  thread reply and clears the working display after the post. Its `include`
  may list only `plect.message` and, during migration, `plect.message_delta`,
  which posts nothing and logs a hint to remove it. Any other event type makes
  the delivery fail.

The three channels take `channel_id` and `thread_ts` and run `slack-adapter`
actions. Query populations use their declared `poll` or `subscribe` means and
`[query]` bindings.

[The review thread example](examples/review-thread-workflow.toml) wires the
thread effect, subscription, and three channels into a workflow.

See [the migration procedure](../../docs/migrations/slack-multi-bot-service.md) for an existing deployment.
