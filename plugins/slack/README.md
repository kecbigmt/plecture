# slack

The Slack adapter service, thread subscriptions, and Slack delivery channels.
The adapter publishes inbound thread messages to subscribed sessions' event
logs. Runtime delivery uses the core dispatcher and workflow channels.

## Contents

- `config/tasks/thread_dir.toml` — declares the session-scoped `thread_dir`
  effect: acquires/releases a bare conversation directory for a Slack
  thread, with no GitHub artifact standing in for the session. See
  "Thread directory" below.
- `config/tasks/slack_thread.toml` — creates one Slack root message through
  `slack-adapter`. Outputs: `thread_ts`, `channel_id`, and `permalink`.
- `config/resources/thread.toml` — declares inputs-free thread subscription
  hooks and the query source for unbound mentions.
- `config/resources/mentions.toml` — declares the workspace-wide bot mention
  subscription.
- `config/channels/slack.toml` — delivers a session event to its Slack
  thread by posting to the `slack-adapter` service's HTTP API. Inputs:
  `base_url` (e.g. `http://127.0.0.1:7890` for its default `listen_addr`),
  `channel_id`, and `thread_ts` (typically wired from whatever task created
  the thread via slack-adapter's `POST /threads`).
- `config/channels/status.toml` — same inputs and delivery mechanics as
  `slack.toml`, but posts to `POST /status` instead of `POST /messages`: the
  event's nonempty text maps to the agent session's `processing` state and
  an empty `plect.status_message` maps to `active`. The text remains in
  the event for other consumers; Slack shows its own processing text. The
  channel also passes optional `turn_id` metadata to reject delayed
  old-turn completion. Bind this channel only to `plect.status_message`.
- `config/channels/stream.toml` — accepts both `plect.message_delta` and
  `plect.message`, and renders either as one live-updating Slack thread
  reply via the `slack-adapter` service's `POST /stream`. Same
  `base_url`/`channel_id`/`thread_ts` inputs as `slack.toml`; `stream_key`,
  `turn_id`, `text`, `index`, and `final` are read straight from the delivered
  event's own fields (`event.metadata.message_id`, `event.metadata.turn_id`
  (empty when the harness supplies none), `event.body`,
  `event.metadata.index`, `event.metadata.final`, the last two defaulting to
  `0`/`true` for a `plect.message`, which carries no chunk sequence of its
  own) rather than declared as inputs, since an `[[event.channel]].inputs`
  binding has no access to the event being delivered
  (`docs/language/values.md`) and only the channel's own action does. This
  channel runs unconditionally (an `exec` action always runs), so it cannot
  itself skip a `plect.message` whose deltas already rendered and closed the
  stream; slack-adapter's `StreamManager` is what keeps that from posting a
  second Slack message under the same channel/thread/message identity — see
  its own comment (`src/slack-adapter/internal/adapter/stream.go`) and
  `src/slack-adapter/README.md`'s `POST /stream` section for the
  chat.startStream/appendStream/stopStream sequencing and the
  fallback-to-a-single-post behavior when a workspace rejects streaming.
- `src/slack-adapter/` — Slack-specific message relay + subscription
  broker. See `src/slack-adapter/CLAUDE.md`.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/slack
```

Building `slack-adapter` requires a Go toolchain (see the Package format
section of `docs/design/plugin-packaging.md`); `plect plugin add`/`update`
builds it automatically.

## Resident-supervised Services

`slack-adapter` is declared as a `[[services]]` in `plugin.toml`,
supervised by `plect serve` (start, crash-restart with backoff, stop with
the resident process). Outbound thread creation and message posting require
only `SLACK_BOT_TOKEN`; `SLACK_APP_TOKEN` is optional and only activates
Socket Mode inbound relay.

## Thread directory

The `thread` resource (`config/resources/thread.toml`) resolves a thread's
Slack permalink — either the root message's own permalink or a reply's
permalink carrying `?thread_ts=<root>&cid=<channel_id>` — to a session named
`slack/<channel_id>-<root thread_ts digits>`, so both forms of the same
thread's permalink resolve to the same session. It also owns the
subscription hooks, so a session whose entry resource is a thread subscribes
on creation and unsubscribes on destroy.

`config/tasks/thread_dir.toml` is the effect a workflow node composes to give
such a session a directory. `setup` creates a bare directory under its
`dirs_root` input (empty means `$HOME/workspace_dirs`) and reports the same
`channel_id`, `thread_ts`, and `permalink` outputs `slack_thread` reports, so
the shipped `slack`/`status` channels bind from a session this effect backs
exactly as they would from one `slack_thread` created. `cleanup` removes the
directory. Dependencies: a POSIX shell — no git, and no `slack-adapter` HTTP
call, so a workflow that wants a repository checkout composes a separate
effect for it. The workflow names the `workspace_dir` output as its
`workdir`:

```toml
[thread_session]
kind     = "workflow"
resource = "official.slack.thread"
workdir  = { from = "nodes.thread_dir.outputs.workspace_dir" }

[[thread_session.nodes]]
id   = "thread_dir"
uses = "official.slack.thread_dir"
```

The `mentions` resource matches `slack://<team_id>/mentions`, where
`team_id` is the Slack workspace ID returned by `auth.test`. Subscribe an
existing session with `plect subscribe slack://T01234567/mentions --session
<session-name>`. Every `app_mention` the bot receives then appends a
`user.emit` to that session's event log, including when the session is down.
Each event includes the thread root, replies since that session's previous
mention in the thread, and the mention. Its metadata includes `channel_id`,
`thread_ts`, and `mention_ts`. A thread subscription remains independent; a
plain reply reaches thread subscribers only. `plect unsubscribe` removes the
mention subscription, and session destroy removes it automatically.

## Thread contract

`slack_thread` posts whatever `root_text` input it is handed as the root
message and reports the adapter permalink, `thread_ts`, and `channel_id` as
outputs. It carries no opinion about what the thread is for — review, or
anything else — and no PR-specific inputs: how the root text is framed is
the composing workflow's concern.

`slack_thread` only creates the thread. A workflow that needs replies adds
`subscription = { from = "self.outputs.permalink" }` to its node. Core then
binds the session through the `thread` resource's subscribe and unsubscribe
actions, and retries a failed hook. A runtime restart or `plect down` leaves a
session-scoped node's subscription intact. Keep `slack_thread`'s `base_url`
pointed at the same resident adapter whose `listen_addr` the resource hook
reads.

`examples/review-thread-workflow.toml` shows the review case: it composes
`root_text` from a PR title, URL, and head sha, then wires `slack_thread`
outputs into the `slack` channel bound with `include =
["plect.judge.recorded"]`. Its conclusion event is the `plect.judge.recorded`
event that plect appends when a judge records a done_when verdict; the body
is the recorded judge reason, so the Slack reply text is exactly the
conclusion the judge recorded, and metadata carries `instance`, `leaf_id`,
`action`, `revision`, `judge_session`, `judge_workflow`, and
`relation`. The posted text is the event body, falling back to the summary
only for legacy events without a body; progress, heartbeats, terminal events,
and GitHub watcher events do not match that binding.

The same example also shows the binding for the `status` channel, bound to
`plect.status_message` with the same three inputs as the `slack` binding
above (`base_url`, `channel_id`, `thread_ts`) — no per-event input, because
an `[event.channel.inputs]` binding resolves from session/node outputs only
and has no access to the event being delivered; only the channel's own
action does (see `channels/status.toml`).

It also shows the `stream` channel bound with `include = ["plect.message",
"plect.message_delta"]` and the same three inputs again. One such binding is
enough for every runtime a workflow composes, streaming or not: a
Claude-style delta-then-message sequence and a Codex-style message-only one
both post through it, and telling a redundant `plect.message` apart from
one that needs posting is `StreamManager`'s job
(`src/slack-adapter/internal/adapter/stream.go`), not the workflow's. A
workflow never needs a separate binding — or a separate channel — per
runtime, and `plect.message` is not reserved for the `slack` channel.

### Agent session setup

The app owner must declare the Slack app as an agent. The existing bot token
already has `assistant:write`, and `chat:write` is in the shipped manifest.
The API migration leaves `agent_view` alone. A
later switch to `agent_view` is irreversible. The owner also verifies the
real Slack display after the adapter deploys. See
`docs/migrations/slack-agent-session-status.md` for the procedure.

## Presentation-only exceptions

`slack-adapter`'s deprecated `/notify` rollback path (see
`src/slack-adapter/CLAUDE.md`) inspects a GitHub-derived `change_type` to
pick an emoji prefix, falling back to a generic prefix for anything else.
This is a display detail of an already-deprecated path, not a workflow
commitment the config layer makes, so it is left as is rather than
generalized.

## Standing sessions (population source)

`config/resources/thread.toml` declares the `thread` resource's
`[thread.query]` face
(`docs/adr/2026-09-05-standing-session-dispatch.md`, "Subscribe-only
observer sketch"): the item source a workflow's `[[workflow.populations]]`
entry binds to keep a session present for a Slack thread that received an
unbound `app_mention`. It declares only `subscribe` — a mention is not
enumerable, so there is no `poll` means, and a population entry using this
observer always sets `expire_after` rather than `poll_every`.

The `official.slack.slack_subscribe` effect in that ADR's standing-session
example is retired. Core automatically subscribes a session's entry thread
resource, and the adapter's pending-mention catch-up publishes the mention
that started the population-created session as `user.emit`. The resource
hooks remain inputs-free.

- **`slack-adapter subscribe unbound-mentions`** (see
  `src/slack-adapter/README.md`) is the subscribe means: it never opens a
  second Socket Mode connection, instead reading the resident adapter's own
  `/unbound-mentions` feed and converting each `unboundMentionItem` straight
  into the query's item shape: `resource` (the mention's permalink,
  required) plus `channel_id`, `thread_ts`, `mention_ts`, and `user_id` (the
  mentioning user's Slack ID) context.
- **Restricting who can start a session.** The query's optional `user_ids`
  (a non-empty array of Slack user IDs, matched exactly) lets only those
  users' mentions through; without it everyone passes. Rejected mentions are
  dropped silently unless a reply is configured: `denied_user_message` is
  posted once into the thread of a mention from a user outside `user_ids`,
  and `denied_channel_message` once into the thread of a mention in a channel
  outside `channel_ids`, e.g. a redirect to the right channel. A failed post
  (such as into a channel the bot has not joined) is logged and not retried.
  A rejected mention never becomes an item, so a workflow never sees it.
  Copyable population entry:

  ```toml
  [[ops.populations]]
  name         = "mentions"
  uses         = ["subscribe"]
  expire_after = "8h"

  [ops.populations.query]
  base_url               = "http://127.0.0.1:7890"
  channel_ids            = ["C01234567"]
  user_ids               = ["U01234567", "U07654321"]
  denied_user_message    = "Sorry, you are not authorized to start a session here."
  denied_channel_message = "Please mention me in #ops instead."
  ```

  A workflow can record who asked with `{ from = "item.user_id" }`.
- **`slack-adapter resource observe`** is `thread`'s `observe` action. The
  language requires every resource to declare one, but this
  observer's `state_schema` is empty — a mention appearance is already the
  only fact `query.subscribe` reports — so it prints `{}` rather than
  fabricating a state key the schema does not declare.

## Not included

- Which agent runtime delivers into Slack, or vice versa — a workflow's
  concern, composed by wiring this plugin's channel and any agent-runtime
  plugin's own tasks/channels together. This plugin never connects to a
  channel-server socket or imports an agent-runtime plugin's package.
- slack-adapter's Slack App manifest, HTTP API surface, and subscription
  broker behavior — see `src/slack-adapter/CLAUDE.md`.

## Compatibility fixtures

`testdata/compat/<tag>/` holds this plugin's task declarations as of the
latest tagged release (`config/tasks/`) and a `scenarios.toml` in the
effect-scenario format. `TestShippedEffects_ReleasedDeclarationsAndRecordsStillRun`
(`go test -tags integration ./app/internal/task`) runs every released
declaration against current executables. Effects still present also run with
the current declaration, so updating the plugin cannot break a session created
under that release. Scripts run as checked in, and the other executables are
built with their manifest `build` command; the programs a declaration launches in a terminal
(the `claude`/`codex` CLI itself) and the system tools stay stand-ins under
`testdata/effects/`.
The scenarios cover the thread and subscription effects. The adapter needs a
Slack connection to run, so `subscribe_request.json` holds the request the
released subscription declaration sends and
`TestReleasedSubscribeWireShapesAreAccepted` in `src/slack-adapter` drives the
adapter's own handlers with it, then lists and deletes the subscription as the
released alive probe and cleanup do.

Refresh the fixtures when a release is tagged:

```bash
tag=$(git describe --tags --abbrev=0)
dir=plugins/slack/testdata/compat
old=$(ls "$dir")
[ "$old" = "$tag" ] || git mv "$dir/$old" "$dir/$tag"
git rm -rqf "$dir/$tag/config"
mkdir -p "$dir/$tag/config"
git archive "$tag" plugins/slack/config/tasks | tar -x -C "$dir/$tag/config" --strip-components=3
git show "$tag:plugins/slack/testdata/effect-invocations.txt" | grep -E '^(==|outputs)'
```

The rest of the directory carries over to the new tag. Set each `self` in its `scenarios.toml` to the `outputs:` line the
last command prints for that effect, so the scenarios carry the shape that
release recorded. When a stand-in under `testdata/effects/`
changes how an executable answers, the released declarations run against the
new answer too; keep them passing rather than editing the fixture.
