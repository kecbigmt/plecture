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
| `[[executables]]` | `name`, `path`, optional `build`, and optional boolean `service` (default `false`). |

An executable's `name` is what a `bin` reference resolves; `path` is relative to
the plugin root. `build` is literal shell, run at install or update time rather
than at session time.

`plugin.toml` rejects `[[services]]`. Resident instances are declared in user
configuration, and each instance supplies its own environment.

## User services

Each `<config-home>/services/*.toml` file contains one or more service tables.
One Slack App uses one table:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-primary.env"
```

Two Slack Apps use two tables, each with distinct credentials and state:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-primary.env"

[[services]]
name = "slack-secondary"
executable = "official/slack/slack-adapter"
env_file = "/etc/plect/service-env/slack-secondary.env"
restart = "never"
health = { type = "process" }
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

`restart` is `on-failure` by default or `never`. `health` defaults to and only
accepts `{ type = "process" }`. Unknown keys, duplicate names, unresolved or
unmarked executables, and invalid values fail configuration loading. The
service gets a private state directory (mode `0700`) and Unix API socket
(mode `0600`) under `<data-home>/services/<name>/`; hooks find the correct
Slack socket through the bot-specific instance registry. Local API callers,
including channels and tools, use Unix-socket clients.

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
- A plugin's `plect_min_version` must not exceed the running `plect`.
