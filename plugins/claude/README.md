# claude

Claude Code launch task, initial-prompt submit/readiness composition,
structured delivery via the channel-server daemon, and the headless
`claude -p` worker/enqueue pair. Split out of the former
`session/runtime` plugin per `docs/design/plugin-boundary-contracts.md`;
`gh-guard` moved to the `github` plugin, and `tmux` is a separate,
independently selectable plugin this one composes through `{ terminal = "..." }` — never a direct dependency.

## Contents

- `config/tasks/runtime.toml` — the `runtime` effect. `setup` launches `claude`
  (fresh or `--resume`, with a same-session-id fallback if resume finds no
  persisted conversation) via `{ terminal = "send_text" }`/`{ terminal = "send_keys" }`, waits for it to come up by polling
  `~/.claude/sessions/*.json`, wires a channel-server MCP socket when
  `channel-server` is on `PATH`, and registers turn-boundary activity and
  turn-reporting hooks (`publish_events`, see Parameters below).
  `[health].alive` checks only the recorded process. A failed check makes the
  next `plect up` clean up and rebuild the effect with its prior session ID.
  Cleanup replaces a live Claude process only when its session record matches
  that ID; it fails before setup when another conversation occupies the pane,
  so a launch command is never submitted as that conversation's message.
  `[health].activity` reads back the record those turn-boundary hooks write,
  so hook and probe are two halves of one fingerprint format.
- `config/tasks/claude_initial_prompt.toml` — sends a session's initial prompt via
  `{ terminal = "..." }` once the CLI's input box is visible, or on every
  `plect up` when `repeat = "true"`.
- `config/channels/delivery.toml` — the `delivery` channel: it delivers a session event to the running
  Claude Code process over its channel-server Unix socket.
- `config/tasks/headless_runtime.toml` + `config/channels/headless_delivery.toml`
  — the headless shape: starts `claude-headless-worker` via
  `{ terminal = "..." }` instead of the interactive TUI, which drains a
  per-session queue directory serially into `claude -p` (`--session-id` on
  the first turn, `--resume` after). A later event is delivered by
  appending to that queue (`claude-headless-enqueue`) rather than through
  channel-server, so there is no input box to wedge and no long-lived
  process to push into. See Headless Runtime below.
- `scripts/claude-headless-worker`, `scripts/claude-headless-enqueue` — the
  worker `headless_runtime` launches and the enqueue `headless_delivery`
  runs, each resolved through `bin = "<name>"` so neither needs a `PATH`
  entry of its own.
- `scripts/claude-mcp-servers` — the author-fixed serialization step behind the
  `runtime` and `headless_runtime` effects' `mcp_servers` parameter: it merges the declared registration
  records into the MCP config that task assembles, and fails the launch on a
  malformed record rather than dropping it.
- `scripts/claude-agent-activity` — the turn-boundary activity fingerprint
  (setting `silence_expected` once a turn ends, and withholding it inside a
  turn: the hook the `runtime` effect registers, and the `probe` verb that
  task declares as its `[health].activity`), and the turn-reporting hooks
  that publish the agent's own text as core events — see Turn Reporting
  below. One executable, one selftest
  (`scripts/claude-agent-activity_selftest.sh`).
- `src/channel-server/` — generic message delivery to Claude Code, with no
  knowledge of message sources (Slack or otherwise) and no reply tool of its
  own (see Turn Reporting below). Its `queue` mode is the bridge
  `headless_runtime` keeps on the same socket protocol. See
  `src/channel-server/CLAUDE.md`.

## Turn Reporting

An agent's Slack thread (or any other consumer of its session events)
reports what the agent said without the agent having to call any tool: the
`runtime` effect's `publish_events` input (default `["message"]`) names
which core events (`plect.message`, `plect.message_delta` — the type names
without their `plect.` prefix) to publish; the same input name and
semantics as every other runtime plugin in this catalog, so a workflow
reads identically across Claude, Codex, and future harnesses. Which Claude
Code hooks that installs is this plugin's own concern:

- `"message"` installs the `Stop` hook. `"message_delta"` installs
  `MessageDisplay`. Both may be requested together.
- `plect.message` is canonical: it is always emitted, exactly once per
  `message_id`. With `"message"` alone, it comes from that turn's
  `last_assistant_message`, with a deterministic `message_id`
  (`message_id_origin = synthetic` — Stop's `prompt_id` when present,
  since that already identifies the turn uniquely within the session; a
  per-session counter on the one boundary Stop exposes no `prompt_id` for
  at all) and `turn_id` from that same `prompt_id`. With `"message_delta"`
  also requested, it is published once a message's final delta arrives
  (full text now known), carrying that delta's own `message_id`
  (`message_id_origin = native`) and `turn_id`; `claude-agent-activity`
  itself then skips the Stop-hook copy for that same message (a marker
  file records which message_id was just covered), so a live consumer and
  a finished-messages consumer never both see the text and never see it
  twice. Metadata otherwise: `role = assistant`, `source = claude`.
- `"message_delta"` also publishes `plect.message_delta` once per streamed
  delta, as a preview: body is that delta alone, metadata carries
  `message_id`, `message_id_origin = native`, `kind = text`, `turn_id`,
  `index`, `final`, `source = claude`. `index` is this hook's own counter
  per `message_id` (0-based, one per delta actually published), not
  Claude's own `index` field from the hook payload, which a future harness
  could give a different meaning to (e.g. a block index).
- An empty message publishes nothing, and neither does an empty *non-final*
  delta. An empty *final* delta is the one exception: it still publishes
  (empty body, `final = true`), because Claude Code's own newline-driven
  delta split routinely lands a message's last hook invocation on empty
  text, and a delta-driven consumer needs an actual `final = true` delta to
  close its stream — it does not necessarily watch `plect.message`, a
  different event type, to know the stream ended. A publish failure
  (`plect` unreachable) never blocks or delays the agent's turn — every
  hook exit path is 0.
- `headless_runtime` registers the same `Stop` hook for `"message"`, since
  hooks fire under `claude -p` too; its schema accepts only `"message"`,
  because `MessageDisplay` rides the TUI's own rendering, which a print-mode
  turn never does.
- A consumer that wants only finished messages includes `plect.message`;
  one that wants a live view includes `plect.message_delta` too and
  replaces its running preview with the `plect.message` for the same
  `message_id` once it arrives.

## Parameters

Author-declared values a workflow sets to steer these configs without
replacing them (the parameterization rung of
`docs/design/task-nesting.md`'s customization ladder):

| Config | Parameter | Meaning |
|---|---|---|
| `tasks/runtime.toml` | `launch_env` | JSON object of environment variables exported on the launch line. Keys must be valid environment variable names; values are shell-quoted. |
| `tasks/runtime.toml` | `mcp_servers` | JSON array of MCP server registration records — `{name, command, args?, env?}` — merged into the `--mcp-config` JSON alongside this task's own registrations. A record only ever reaches the agent's config file, never a command line; a name that collides with a registration the task already made, or a record missing `name`/`command`, fails the launch. |
| `tasks/runtime.toml` | `launch_timeout` | How long the launch poll waits for `claude`'s session file to register, as a `"<seconds>s"` token. Default `120s`. On timeout, or any other non-zero setup exit after the process was started, the pane's `claude` child is terminated before the node fails, so a retry never types into a still-live process. |
| `tasks/runtime.toml` | `permission_mode` | `--permission-mode <value>` on the launch line, one of `claude`'s own mode names or `""` to omit the flag. Default `bypassPermissions`, so a dispatched session never blocks on the CLI's first-run "Set up auto mode?" wizard. The default also seeds `skipDangerousModePermissionPrompt` into the hooks settings file, since `bypassPermissions` itself carries a one-time interactive disclaimer on an account/host that has never accepted it. |
| `tasks/runtime.toml` | `publish_events` | Array of core event names to publish (`"message"`, `"message_delta"`), without their `plect.` prefix. Default `["message"]`. See Turn Reporting above. |
| `tasks/headless_runtime.toml` | `model`, `effort`, `permission_mode`, `launch_env`, `mcp_servers`, `path_prepend`, `launch_timeout` | As for `runtime`, applied to every `claude -p` turn rather than one launch line. `launch_timeout` bounds the wait for the worker's readiness, and `permission_mode` still defaults to `bypassPermissions`, since `-p` denies every tool call nothing is present to approve. |
| `tasks/headless_runtime.toml` | `api_key_file` | Path to a file holding an Anthropic API key. The worker reads it on every turn and passes it to `claude -p` alone as `ANTHROPIC_API_KEY`; it never reaches the pane's keystrokes. Empty = the CLI's own authentication. An unreadable or empty file fails the turn rather than falling back to another login. |
| `tasks/headless_runtime.toml` | `state_root` | Directory the worker's per-session queue and state live under. Empty = a temporary directory. |
| `tasks/headless_runtime.toml` | `publish_events` | `["message"]` or `[]`. Default `["message"]`. |
| `channels/headless_delivery.toml` | `enqueue_timeout` | Per-attempt delivery deadline. Default `5s`. |
| `channels/headless_delivery.toml` | `message_envelope` | Format of the queued message. Placeholders: `{type}`, `{body}`, `{summary}`, `{body_or_summary}`, `{url}`, `{url_suffix}`. Default `[{type}] {body_or_summary}{url_suffix}`. |

These are set on the node or channel binding that selects the declaration, as
values over the workflow surface's own roots. A user-owned workflow names a
plugin's declaration by its catalog address — the alias you enabled this plugin
under, then its plugin path, then the declaration's id:

```toml
[[my_workflow.nodes]]
id   = "agent"
uses = "official.claude.runtime"

[my_workflow.nodes.inputs]
launch_env   = '{"PLECT_TEAM_CONTEXT":"acme"}'
mcp_servers  = '[{"name":"kbn","command":"kbn-mcp","args":["--scoped"]}]'

[[my_workflow.event.channel]]
name    = "runtime"
uses    = "official.claude.delivery"
include = ["plect.instruction", "user.emit"]

[my_workflow.event.channel.inputs]
path = { from = "nodes.agent.outputs.socket_path" }
```

`docs/language/workflows.md` specifies that surface and
`docs/language/declarations.md` the reference grammar; substitute your own alias
for `official`.

## Headless Runtime

`headless_runtime` runs each turn as its own `claude -p` process under one
session id, so a session's conversation still carries across turns while no
interactive Claude Code session ever exists. That distinction is what
decides billing: with `api_key_file` set, every turn is an API request made
with that key, billed to the key's own API organization (including any
monthly API credits granted to it), not to an interactive Claude Code
subscription's usage limits.

```toml
[[my_workflow.nodes]]
id   = "agent"
uses = "official.claude.headless_runtime"

[my_workflow.nodes.inputs]
model        = "opus"
api_key_file = "/etc/plect/anthropic-api-key"
state_root   = "/var/lib/plect/claude-headless"

[[my_workflow.event.channel]]
name    = "runtime"
uses    = "official.claude.headless_delivery"
include = ["plect.instruction", "user.emit"]

[my_workflow.event.channel.inputs]
queue_dir = { from = "nodes.agent.outputs.queue_dir" }
```

- Without `api_key_file`, turns use whatever login the CLI finds. To bill a
  Console organization through a login rather than a key file, give the
  runtime its own Claude Code configuration directory — the CLI scopes its
  stored credentials to that directory, so interactive sessions keep their
  own login — and sign in there once with
  `CLAUDE_CONFIG_DIR=<dir> claude auth login --console`. Then set
  `launch_env = '{"CLAUDE_CONFIG_DIR":"<dir>"}'` on the node. That
  directory also replaces the user-level settings and memory the CLI would
  otherwise load.
- When `channel-server` is built, the worker also keeps a
  `channel-server queue` bridge listening on the per-session socket the
  interactive runtime would use, and setup reports it as `socket_path`. An
  adapter that speaks the channel-server protocol — the slack plugin's
  `slack_subscribe`, wired to `nodes.agent.outputs.socket_path` — reaches
  the queue unchanged. Each message becomes its own turn, wrapped in the
  same `<channel source="channel-server" user="…" user_id="…" thread_ts="…">`
  tag an interactive session shows. A bridge that exits is relaunched, and
  stopping the worker stops it.
- A print-mode turn never raises a permission request, so nothing is relayed
  for approval: a `y <id>` reply reaches the agent as ordinary text. Choose
  `permission_mode` (default `bypassPermissions`) with that in mind.
- Turns are drained one at a time; an event arriving mid-turn waits in the
  queue rather than interrupting the running turn.
- `health.alive` fails once the worker dies or its last turn exited
  non-zero, so an exhausted or revoked key surfaces as an unhealthy
  instance rather than a silently idle one. Each turn's stdout and stderr
  land under `<state_dir>/log/`.
- Stopping the worker (`cleanup`) also stops a turn still running, so a
  torn-down session stops spending.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/claude
```

Building `channel-server` requires a Go toolchain (see the Package format
section of `docs/design/plugin-packaging.md`); `plect plugin add`/`update`
builds it automatically.

A session using this plugin also needs a `[terminal]`-declaring task in the
workflow (e.g. `official/tmux`'s `tmux` task) — this plugin's own tasks
never declare `[terminal]` themselves.

## Resident-supervised Services

`channel-server` is declared as a `[[services]]` in `plugin.toml`,
supervised by `plect serve` (start, crash-restart with backoff, stop with
the resident process). Its declaration stays inert today by design: real
instances are per-session, launched by Claude Code itself via MCP
configuration with a session-specific `CHANNEL_SOCKET_PATH` the resident
process never has — see `plugin.toml`'s comment and
`docs/adr/2026-08-16-plugin-service-lifecycle.md`'s Consequences section.

## Not included

- Which agent CLI a session launches beyond Claude Code, and any model/
  effort defaults for other CLIs — a workflow's concern.
- The write guard for `gh` — see the `github` plugin's `gh_guard` task; this
  plugin's `runtime` effect accepts only a generic `path_prepend` input, never
  a GitHub-specific switch.
- A no-channel-server interactive Claude configuration is outside the
  supported surface — see `docs/migrations/` for the migration procedure.
