# Plugin and catalog manifests

A plugin is a distributable package: executable adapters, config resources, and
metadata. Its manifest and its catalog's manifest are TOML, but they are not
definition documents — they sit outside a definition root and carry no `kind`.

## plugin.toml

`plugin.toml` lives at a plugin's root and declares package metadata and the
executables it owns. A resident-capable executable carries a `service` marker;
the marker does not start it.

```toml
schema_version    = 3
version           = "0.1.0"
plect_min_version = "0.0.0"
description       = "GitHub resource, workspace effect, and watcher subscription."

[[executables]]
name  = "github-worktree"
path  = "bin/github-worktree"
build = "go -C src build -o ../bin/github-worktree ./cmd/github-worktree"

[[executables]]
name = "gh-guard"
path = "scripts/gh-guard"

[[executables]]
name = "github-watcher"
path = "bin/github-watcher"
service = true
```

| Field | Meaning |
|---|---|
| `schema_version` | Manifest format, so a manifest is identifiable before anything else is read. |
| `version` | The package's own version. |
| `plect_min_version` | The lowest `plect` this plugin loads against. |
| `description` | What the package is for. |
| `[[executables]]` | `name`, `path`, optional `build`, optional boolean `service` (default `false`), and optional inline `config_schema` for a service executable. |

An executable's `name` is what a `bin` reference resolves; `path` is relative to
the plugin root. `build` is literal shell, run at install or update time rather
than at session time.

`plugin.toml` rejects `[[services]]`. Resident instances are declared in user
configuration, and each instance supplies its own environment.

## User services

Each `<config-home>/services/*.toml` file contains one or more service tables.
One executable may appear in only one declaration. In this example,
`<config-home>` is `/srv/plect/config`; one Slack service serves two bots:

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

`name` is unique across all service files. It starts with a lowercase ASCII
letter, contains only lowercase letters, digits, and hyphens, and is at most
64 characters. `executable` is a fully qualified
`<catalog>/<plugin>/<executable>` reference to an enabled executable marked
`service = true`. `args` is an optional string array, defaulting to empty.
`env_file` is an absolute path to an owner-readable file of literal
`KEY=VALUE` lines. Blank and `#` comment lines are ignored. Duplicate keys,
invalid names, malformed lines, unreadable files, and relative paths fail
loading. Interpolation is not allowed. A conflicting `PLECT_*` runtime key
fails loading. The service receives only env-file entries and plect runtime
values, and validates its own required inputs.

`[services.config]` is optional, non-secret plugin-specific configuration.
The executable declares its `config_schema` inline in `plugin.toml`, using a
JSON Schema-shaped TOML table. A config table without a schema or one that
fails validation is a load error. Core does not interpret its fields. The
Slack schema requires a nonempty `bots` array; each bot requires string
`app_token_env`, `bot_token_env`, and `channel_id` fields and an
`allowed_user_ids` string array, with no extra fields. Token fields name keys
in `env_file`; their values remain there. The adapter rejects duplicate
`channel_id` values across bots. On every service executable invocation,
including a node, channel, or hook action, plect overlays the service env
values on the action's ordinary environment and sets `PLECT_SERVICE_CONFIG`
to the validated non-secret config JSON itself. Service values win
duplicate keys; an undeclared service executable fails invocation by name.

`restart` defaults to `on-failure` and may be `never`. `health` defaults to
and only accepts `{ type = "process" }`. The supervised service gets only its
`env_file` and plect runtime values. It receives `PLECT_DATA_DIR` set to the
mode-`0700` `<data-home>/plugins/<plugin>/` directory for authoritative state;
actions never receive it. The data home resolves from `--data-home`, then
`PLECT_DATA_HOME`, then `$XDG_DATA_HOME/plect`, then `~/.local/share/plect`.
The service inherits neither `XDG_*` nor `HOME` from the supervisor and gets
no global data or config home. Plugins receive none of `PLECT_CONFIG_HOME`,
`PLECT_DATA_HOME`, or `PLECT_CACHE_HOME`. The service writes JSON-lines
`publish` requests to stdout; its supervising plect
echoes each request's required `id` in a result such as
`{"op":"published","id":"request-1","delivered":1}` or an error such as
`{"op":"error","id":"request-1","code":...,"message":...}`. It sends a full
`subscriptions` snapshot to stdin at start, followed by add/remove deltas;
these notifications carry no `id`. Logs go to stderr. It never calls the
plect CLI. No local control listener exists. Browser content on the same host
and other OS users cannot reach one; same-user code is outside this boundary.
The env file is owner-readable and `PLECT_SERVICE_CONFIG` contains only
validated non-secret JSON. Slack outbound actions call the provider Web API
directly.

Every plugin executable invocation receives `PLECT_CACHE_DIR` set to
`<cache-home>/plugins/<plugin>/`, including services and node, channel, and
hook actions such as `observe`. The directory has mode `0700`; its contents
are disposable, may be deleted wholesale by plect, and require plugin-owned
locking. Core never reads them. The cache home resolves from `--cache-home`,
then `PLECT_CACHE_HOME`, then `$XDG_CACHE_HOME/plect`, then `~/.cache/plect`.
Authoritative subscription membership stays in core, and per-resource watch
state stays in the service's `PLECT_DATA_DIR`.

Core keys subscriptions by `(session, resource, source)`. `plect subscribe`
adds an idempotent `runtime` row; `plect unsubscribe` removes only that row.
`plect up <url>` adds a `dispatch` row for the entry resource, removed on
session destroy. A resource-bound task adds a `task` row while any task
instance in that session remains bound to the resource. A node `subscription`
adds a `node` row while any live node binds that resource. A resource's
`max_subscribers` counts distinct sessions,
and delivery happens once per session/resource. Core publishes to subscribed
sessions and sends each service subscription snapshots and deltas by stdio.

The Slack bot-user resource `slack://<team_id>/<bot_user_id>` allows one
subscriber. An allowed mention is delivered as `user.emit` to a subscribed
thread, then to the bot-user subscriber if the thread delivery count is zero;
otherwise it is dropped and logged. The adapter handles denied messages.
Slack thread `query.subscribe` and `/unbound-mentions` are absent. Creating a
session for each new mention thread is deferred until a use case exists.
Population `uses`, `[query]`, and executable query means remain as specified
in [workflows](workflows.md).

The GitHub watcher receives subscription snapshots and deltas through stdio.
Standalone issue and pull-request observation needs no watcher service; it
uses action-process credentials. The watcher and standalone `observe` share
disposable App token and rate budget caches through `PLECT_CACHE_DIR`. GitHub
query populations retain their poll form.

`plect service list` shows declared name, executable, state, pid, and restart
count. With `--all`, an enabled service-marked executable with no declaration
appears once as `available`, with an empty name and pid:

```text
NAME   EXECUTABLE                         STATE      PID    RESTARTS
slack  official/slack/slack-adapter       running    4102   0
       official/github/github-watcher     available
```

## catalog.toml

`catalog.toml` lives at a catalog root and declares which plugins the catalog
publishes.

```toml
schema_version = 2
description    = "Plecture's official plugin catalog: reusable resource, effect, task, and channel packs."

plugins = [
  "tmux",
  "claude",
  "codex",
  "slack",
  "okf",
  "github",
]
```

Explicit enumeration, not directory presence, is what publishes a plugin. A
directory inside the catalog's trust space that the manifest does not list is
not catalog-addressable — nor is a listed plugin's own build source.

## Config resources

A plugin's `config/` directory is its definition root. Its layout is author
organization only: one definition per file, or kind-named subdirectories, mean
the same thing. See [`declarations.md`](declarations.md).

A plugin's definitions form one id namespace across all kinds, and every
reference written inside a plugin is relative.

## Validation rules

- `schema_version`, `version`, `plect_min_version`, and `description` are
  required in a plugin manifest.
- `schema_version` and `plugins` are required in a catalog manifest.
- An executable `name` is unique within a manifest.
- A service's `executable` names a declared executable.
- A service config table requires the executable's `config_schema` and must
  satisfy it; the schema is inline in that executable entry.
- Plugin authors keep secret settings out of service config schemas; plect
  cannot identify secret fields by name or type.
- A plugin's `plect_min_version` must not exceed the running `plect`.
