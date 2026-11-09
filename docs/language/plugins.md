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
| `[[executables]]` | `name`, `path`, optional `build`, optional boolean `service` (default `false`), and optional `config_schema` path for a service executable. |

An executable's `name` is what a `bin` reference resolves; `path` is relative to
the plugin root. `build` is literal shell, run at install or update time rather
than at session time.

`plugin.toml` rejects `[[services]]`. Resident instances are declared in user
configuration, and each instance supplies its own environment.

## User services

Each `<config-home>/services/*.toml` file contains one or more service tables.
In these examples, `<config-home>` is `/srv/plect/config`. One Slack App uses
one table:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-primary.env"

[services.config]
channel_id = "C0123"
mention_session = "operations/_orchestrator"
allowed_user_ids = ["U0123"]
```

Two Slack Apps use two tables, each with distinct credentials and state:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-primary.env"

[services.config]
channel_id = "C0123"
mention_session = "operations/_orchestrator"
allowed_user_ids = ["U0123"]

[[services]]
name = "slack-secondary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-secondary.env"
restart = "never"
health = { type = "process" }

[services.config]
channel_id = "C0456"
allowed_user_ids = ["U0456"]
```

`name` is a unique operator-chosen identity across all service files. It begins
with a lowercase ASCII letter, then contains only lowercase letters, digits,
or hyphens, and is at most 64 characters. `executable` is the fully qualified
`<catalog>/<plugin>/<executable>` reference to a `service = true` executable
in an enabled plugin. `args` is an optional string array, defaulting to empty.
`env_file` is an absolute path to an operator-owned file of literal
`KEY=VALUE` lines. Blank lines and `#` comment lines are ignored. The file is
owner-readable only. Duplicate keys, invalid names, malformed lines,
unreadable files, and relative paths are errors. Interpolation is not allowed.
The service receives only env-file entries and plect-provided runtime values;
its executable checks its own required inputs.

`[services.config]` is optional plugin-specific, non-secret configuration.
A service executable that uses it declares a plugin-relative `config_schema`
path in its manifest. plect validates the table against that JSON Schema,
writes it privately under the instance state directory, and passes its path
as `--config`. A table without a schema or a schema violation fails loading;
core does not interpret the settings. Secrets go in `env_file` only. For a
Slack service, `mention_session` may be omitted. It is the one
default-recipient field: a mention goes first to subscribed thread sessions,
then to every durably registered unbound-mention population whose
`channel_ids` contains its channel, then to `mention_session` if set. Without
any recipient, the adapter drops the mention and logs its channel, thread,
and reason. Registrations persist consumer identity, channel filter, pending
mentions, and delivery watermark across disconnects. The population
acknowledges each item after recording its appearance durably; removing the
population explicitly unregisters it. A default and registrations may
coexist on one instance. A later user filter or failed admission does not
fall through to the default.

`restart` is `on-failure` by default or `never`. `health` defaults to and only
accepts `{ type = "process" }`. Unknown keys, duplicate names, unresolved or
unmarked executables, and invalid values fail configuration loading. The
service gets a private state directory (mode `0700`) and Unix API socket
(mode `0600`) under `<data-home>/services/<name>/`. plect supplies
`PLECT_SERVICE_STATE_DIR` and `PLECT_SERVICE_SOCKET` to the service; env files
cannot override these runtime values. Service state stays private. Hooks,
channels, tasks, and service-backed population queries name a declared
service; plect resolves the name to its socket for the action. Unknown literal
names fail config validation; dynamic unknown names and stopped services fail
requests for retry. Slack channels remain `exec` channels invoking
`curl --unix-socket` with the resolved path.

For a per-thread Slack population, the query names its instance and passes
the same name to its sessions:

```toml
[[thread_work.populations]]
name = "mentions"
uses = ["subscribe"]
expire_after = "8h"

[thread_work.populations.query]
service = "slack-secondary"
channel_ids = ["C0456"]

[thread_work.populations.session.inputs]
service = { from = "query.service" }
```

The query registers its channel filter on that service and consumes its
durable unbound-mention feed. Its consumer identity is the workflow population
identity, independent of a live connection. Each created session subscribes
to its thread on the same service. A per-PR population instead
polls matching pull requests and uses a webhook receiver for early arrivals;
its sessions name the GitHub watcher service for subscription delivery:

```toml
[[pull_review.populations]]
name = "labeled_pulls"
uses = ["poll", "subscribe"]
poll_every = "5m"

[pull_review.populations.query]
service = "github-reviews"
repositories = ["example/project"]
labels = ["agent-review"]
state = "open"
draft = false

[pull_review.populations.session.inputs]
service = { from = "query.service" }
```

`github-reviews` is a declared `official/github/github-watcher` service.
The polling helper uses its token and rate budget API; GitHub is the
membership authority. Each review session's subscribe/unsubscribe hooks
use the same service's socket API.
Standalone issue and pull-request observation needs no watcher service. It
uses action-process credentials and a process-local rate budget and App token
cache, so it can mint another token and does not share rate-limit coordination
with a resident watcher. The Slack thread query schema requires `service` and
`channel_ids` and does not accept `base_url`; the GitHub pull-request query
schema requires `service`, `repositories`, `labels`, `state`, and `draft`.
Both reject unknown keys.

`plect service list` shows declared name, executable, state, pid, and restart
count. With `--all`, an enabled service-marked executable with no declaration
appears once as `available`, with an empty name and pid:

```text
NAME             EXECUTABLE                           STATE      PID    RESTARTS
slack-primary    official/slack/slack-adapter         running    4102   0
slack-secondary  official/slack/slack-adapter         running    4103   1
                 official/github/github-watcher       available
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
  satisfy it; the schema path stays within the plugin package.
- Plugin authors keep secret settings out of service config schemas; plect
  cannot identify secret fields by name or type.
- A plugin's `plect_min_version` must not exceed the running `plect`.
