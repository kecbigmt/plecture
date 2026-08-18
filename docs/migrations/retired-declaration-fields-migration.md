# Retired declaration fields migration

This migration covers the retirements decided in
`docs/adr/2026-08-18-retire-dormant-declaration-fields.md`: the task-definition
fields `primary`, `idle_after`, and `execution`; the channel and resource
`execution` fields; the workflow-level `environment` and `environment_inputs`
fields; and the `environment` declaration kind (`environments/*.toml`).

This repository's own shipped catalog is already migrated as part of that
change — the `primary = true` declarations on the `claude`, `codex`, and
`codex_exec` tasks are gone, and no shipped plugin ever declared any of the
other keys. This migration is for user-owned configuration: a global
`~/.config/plect/` layer, a repo overlay, or a third-party plugin.

The change is intentionally breaking. Plecture is pre-1.0, so authors migrate
their own configuration once instead of relying on a compatibility read.
Declaring any retired key is a load error naming that key, rather than a key
the decoder silently drops.

## Backup

Before editing anything, copy each affected file to a timestamped backup:

```bash
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
for CONFIG_FILE in \
  ~/.config/plect/tasks/*.toml \
  ~/.config/plect/workflows/*.toml \
  ~/.config/plect/channels/*.toml \
  ~/.config/plect/resources/*.toml \
  ~/.config/plect/environments/*.toml \
  .plect/workflows/*.toml
do
  [ -f "$CONFIG_FILE" ] || continue
  cp "$CONFIG_FILE" "$CONFIG_FILE.migration-backup.$STAMP"
done
```

Substitute your actual configuration directories (a plugin's `config/`, the
global `~/.config/plect/`, or a repo overlay's `.plect/`) for the globs above.

## Find every declaration

```bash
grep -rnE '^[[:space:]]*(primary|idle_after|execution|environment|environment_inputs)[[:space:]]*=|^\[environment_inputs\]' \
  ~/.config/plect .plect
```

## Delete `primary` and `idle_after` from task definitions

Both are ordinary line deletions: nothing read either field, so removing the
line changes no behavior.

Before:

```toml
scope      = "run"
primary    = true
idle_after = "30m"
setup      = "..."
```

After:

```toml
scope = "run"
setup = "..."
```

## Delete `execution` from task, channel, and resource definitions

Every declaration resolved to the host plane already (see below for why), so
deleting the line changes no behavior either.

Before:

```toml
scope     = "session"
setup     = "..."
execution = "host"
```

After:

```toml
scope = "session"
setup = "..."
```

## Delete `environment` / `environment_inputs` from workflows, and the `environments/` directory

A workflow declaring `environment = "<id>"` ran its task setup and cleanup
inside that environment's `exec` wrapper. There is no replacement surface: task
hooks run on the host.

Before:

```toml
name               = "dev"
workspace_provider = "github"
environment        = "docker"

[environment_inputs]
image = "myimage:latest"

[[nodes]]
id = "work"
```

After:

```toml
name               = "dev"
workspace_provider = "github"

[[nodes]]
id = "work"
```

Then delete the environment definitions themselves — the loader no longer reads
the directory, so a leftover is inert rather than an error, but it is dead
weight that reads as live configuration:

```bash
rm -rf ~/.config/plect/environments
```

If a workflow's tasks genuinely need to run inside a container or on a remote
host, that work now belongs to the task's own setup script (which can invoke
`docker exec`, `ssh`, or equivalent directly) until the redesigned execution
plane lands.

### If a task hook referenced `.Environment.outputs`

The `@environment` pseudo-node and its `.Environment.outputs.<key>` template
surface are gone with the kind. A template still referencing it fails to render
with a missing-key error. Move whatever the environment's setup emitted into the
task hook that needs it — either a normal node output that downstream nodes
read as `.Nodes.<id>.outputs.<key>`, or a value the hook computes for itself.

## Verify

Loading is the check: every retired key is a load error naming that key.

```bash
plect workflow list
```

A clean listing means nothing in your layers still declares a retired key.
