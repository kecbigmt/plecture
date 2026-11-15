# Slack plugin

The `slack-adapter` service holds one Socket Mode connection per configured
bot. Plecture owns resource subscriptions and delivers events through the
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
[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_PRIMARY"
bot_token_env = "SLACK_BOT_TOKEN_PRIMARY"
channel_id = "C0123"
allowed_user_ids = ["U0123"]

[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_SECONDARY"
bot_token_env = "SLACK_BOT_TOKEN_SECONDARY"
channel_id = "C0456"
allowed_user_ids = []
```

The owner-readable env file supplies the named tokens as `KEY=VALUE` lines.
The service requires at least one bot, rejects duplicate channel IDs and
missing token bindings, and selects the outbound bot by channel ID. An empty
`allowed_user_ids` allows everyone. Optional `denied_user_message` and
`denied_channel_message` send one reply per bot, channel, thread, and user;
empty values silently drop denied mentions.

## Resources and mentions

`config/resources/thread.toml` resolves a root or reply permalink to the same
thread session. The bot resource, `slack://<team_id>/<bot_user_id>`, permits one
distinct subscriber. A session can use either resource as its entry resource
or subscribe with `plect subscribe`.

For an allowed mention, the adapter publishes `user.emit` to the thread
resource. If that reaches no session, it publishes to the bot resource. If
neither has a subscriber, it logs the drop. A publish error stops routing.
Denied mentions are handled before publication.

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
