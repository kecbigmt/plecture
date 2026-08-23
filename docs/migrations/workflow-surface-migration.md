# Workflow surface migration

The workflow is the last runtime surface cut over to the ratified
configuration language (`docs/language/workflows.md`, `plecture.schema.json`).
A `workflows/*.toml` file is now a definition document: a `[<id>]` table
declaring `kind = "workflow"`, whose `[display]`, node `inputs` and
`[[event.channel]]` `inputs` are **values** rather than Go templates, and
whose `[tick]` and `[healthcheck]` tables carry a closed set of fields.

Every session is dispatched through a workflow, so a workflow that does not
load takes `plect create`, `up`, `down`, `destroy`, `ls`, `status`, `attach`,
`workflow list/show`, the MCP discovery tools and the tick reactor with it.
A pre-migration file fails with `PLECTURE-CFG-KIND-MISSING`.

Only configuration you authored yourself needs this procedure. No plugin in
the catalog ships a workflow; if one you have mounted does, run
`plect plugin update` once its catalog has migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

A repository overlay (`.plect/workflows/` above or inside a workspace
directory) is version-controlled where it lives; convert it in its own repo.

## Identity moves from the filename to the table

The id was the filename stem. It is now the definition table's name, and the
filename is free-form — so a rename is a TOML edit, and one file may declare
several workflows. Two consequences:

- **An id admits no hyphen** (`^[A-Za-z_][A-Za-z0-9_]*$`), because a workflow
  id reaches a session name and a node id has to stay an addressable path
  segment. `coding-claude.toml` becomes `[coding_claude]`.
- A session already created under the old id keeps that id in `state.json`.
  Rename the id only when no live session names it; `plect ls` shows which
  workflow each session is frozen to.

## Convert each declaration

Before:

```toml
# workflows/coding.toml
name        = "Coding agent"
description = "Spawn tmux + an agent, then deliver the initial task."

workspace_provider = "github"

[display]
title  = "{{.Workflow.outputs.title}}"
status = "{{.Workflow.outputs.pr_state}}"

[workspace_provider_inputs]
delete_branch_default = "true"

[[nodes]]
uses = "tmux"

[[nodes]]
uses                = "agent"
inputs.tmux_session = "{{.Nodes.tmux.outputs.session_name}}"
inputs.model        = '{{get .SessionInputs "model" "sonnet"}}'

[tick]
on        = ["github.*"]
heartbeat = "15m"

[[event.channel]]
name        = "runtime"
uses        = "agent_delivery"
inputs.path = "{{.Nodes.agent.outputs.socket_path}}"
include     = ["plect.instruction", "github.*", "user.emit"]
```

After:

```toml
# workflows/coding.toml — the filename is no longer the id
[coding]
kind               = "workflow"
name               = "Coding agent"
description        = "Spawn tmux + an agent, then deliver the initial task."
workspace_provider = "github"

[coding.display]
title  = { from = "workflow.outputs.title" }
status = { from = "workflow.outputs.pr_state" }

[coding.workspace_provider_inputs]
delete_branch_default = "true"

[[coding.nodes]]
uses = "tmux"

[[coding.nodes]]
uses                = "agent"
inputs.tmux_session = { from = "nodes.tmux.outputs.session_name" }
inputs.model        = { from = "session.inputs.model", default = "sonnet" }

[coding.tick]
on        = ["github.*"]
heartbeat = "15m"

[[coding.event.channel]]
name        = "runtime"
uses        = "agent_delivery"
inputs.path = { from = "nodes.agent.outputs.socket_path" }
include     = ["plect.instruction", "github.*", "user.emit"]
```

The mechanical rules the earlier surface migrations state apply here too: the
id is the table name, every field and nested table moves under it, and the
schemas keep their shape. A top-level key written after a `[table]` header is
parsed **into** that table, so `[<id>]`'s own keys come before any nested
table.

### Template variable to root

| Was | Becomes | Available in |
|---|---|---|
| `{{.Workflow.outputs.<key>}}` | `{ from = "workflow.outputs.<key>" }` | `display`, node `inputs`, channel `inputs` |
| `{{.SessionInputs.<key>}}` | `{ from = "session.inputs.<key>" }` | as above |
| `{{get .SessionInputs "<key>" "<d>"}}` | `{ from = "session.inputs.<key>", default = "<d>" }` | as above |
| `{{.Nodes.<id>.outputs.<key>}}` | `{ from = "nodes.<id>.outputs.<key>" }` | node `inputs`, channel `inputs` |
| `{{.SessionName}}` | `{ from = "session.name" }` | node `inputs`, channel `inputs` |
| `{{.ParentSession}}` | `{ from = "session.parent" }` | node `inputs`, channel `inputs` |
| `{{.WorkspaceDirPath}}` | `{ from = "workspace.dir" }` | node `inputs`, channel `inputs` |
| `{{.Branch}}` | `{ from = "workspace.branch" }` | node `inputs`, channel `inputs` |

Each surface observes only its own roots, so a projection of the wrong one is
a load error rather than an empty value. `[display]` is the narrow one: it
reads persisted outputs and the session's inputs and nothing else, because a
listing must cost no network. A display value that reached for
`nodes.<id>.outputs.*` or `session.name` moves the fact it wants into the
workflow's own outputs, or drops it.

A value that needs a string built from several parts is an expression rather
than a projection:

```toml
[coding.display]
title = { expr = "workflow.outputs.title + ' (' + workflow.outputs.pr_state + ')'" }
```

**Check every mixed template by hand.** A value that was a template with
surrounding text — `title = "{{.Workflow.outputs.owner}} orchestrator"` — is a
valid *literal* on the new surface, so it loads and then renders the template
text verbatim instead of the value it used to. Nothing rejects it: a literal
string is what that surface accepts. Search for a `{{` that survived the
conversion before trusting a display:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn '^[^#]*{{' "$CONFIG_HOME"/workflows/ && echo "the lines above still carry template syntax"
```

(The pattern skips a commented-out line, which a workflow that keeps a
disabled node or channel in place has plenty of.)

Each becomes an `expr`:

```toml
[orchestrator.display]
title = { expr = "workflow.outputs.owner + ' orchestrator'" }
```

### Absence is now explicit

A projection whose source has nothing to report is a **contract statement,
not an empty string**: it fails unless the value declares `default` or
`optional = true`. This is what `{{get ... "default"}}` was for, and it is
the one rewrite that changes behaviour rather than spelling. Two forms, with
different meanings:

```toml
[[coding.nodes]]
uses = "agent"
# The key is always handed over, with this value when the session declares none.
inputs.model  = { from = "session.inputs.model", default = "sonnet" }
# The key is omitted entirely when the session declares none, so the effect's
# own `inputs_schema` default applies instead.
inputs.effort = { from = "session.inputs.effort", optional = true }
```

A node input that existed only to pass an empty string through — the shape
`inputs.slack_thread_ts = ""` had — is a literal and stays one; nothing about
a literal changed.

An input that only wires a dependency edge and is never read still needs to
resolve, so give it a default:

```toml
inputs._dep_envfile = { from = "nodes.envfile.outputs._link", default = "" }
```

### Nodes

- **`uses` is required.** It was optional, defaulting to `id`. A node that
  named only `id` now names it through `uses` — `id` is what may be omitted.
- **`id` defaults to the referenced definition's id**, which for a
  catalog-qualified reference is its last segment: `uses = "official.tmux.pane"`
  is addressed as `nodes.pane.outputs.*`. Name an explicit `id` when one
  workflow instantiates the same effect twice.
- **A node's fields are `id`, `uses`, `inputs`, and `blocks`.** Anything else
  is a load error; there is still no `depends_on` — the dependency graph is
  derived from the `nodes.<id>.outputs.*` projections in the wiring, and a
  cycle in it is `PLECTURE-CFG-WORKFLOW-CYCLE` at load.

### Provider parameters are literal data

`[<id>.workspace_provider_inputs]` values are literals: the provider's hooks
run before any workspace or node output exists, so there is nothing for a
projection to read. A tagged value there is
`PLECTURE-CFG-VALUE-TAG-SURFACE`. The values are no longer restricted to
strings — the provider's own `inputs_schema` decides each parameter's type —
so a provider declaring `{ "type": "boolean" }` accepts `true` rather than
`"true"`.

## Rules that tightened

- **A field outside the workflow surface is a load error**
  (`PLECTURE-CFG-FIELD-UNKNOWN`), which is how the retired `environment`,
  `environment_inputs` and workflow-level `done_when` now report themselves.
  The surface is `name`, `description`, `workspace_provider`,
  `workspace_provider_inputs`, `display`, `auto_select`, `nodes`, `event`,
  `tick`, `healthcheck`, `inputs_schema`, and `inputs_schema_file`.
- **`[tick]` and `[healthcheck]` are closed tables.** `[tick]` is `on`,
  `heartbeat`, `max_heartbeat`; `[healthcheck]` is `period`,
  `stall_threshold`, `renotify_every`. A misspelled key is a load error
  rather than a declaration with no consumer, which is how the retired
  `tick.stale_when`, `tick.max_stale_when` and `[tick.movement_source]`
  report themselves. (An activity probe is an effect-level `[health].activity`
  declaration.)
- **The cascade appends only `nodes`.** A deeper layer may set a field the
  shallower layer left unset, its nodes append, and its `[tick]` /
  `[healthcheck]` replace the shallower table wholesale — but any other field
  the shallower layer already set is a redeclaration error. Two shapes that
  used to work stop working:
  - **`[[event.channel]]` entries no longer append across layers.** A layer
    stack that split its channels — a base `runtime` channel plus an overlay's
    `slack` channel — declares them together in whichever layer owns the
    workflow.
  - **`inputs_schema` is no longer combined with `allOf`.** Two layers each
    declaring one used to be intersected; the merged contract is now stated by
    one layer.
- **A workspace-dir overlay may declare `nodes` and nothing else.** The rule
  is stated as an allowlist rather than a list of forbidden fields, so
  `workspace_provider_inputs` — which the old check happened to miss — is
  rejected there too. `.plect/workflows/` inside a workspace directory is
  cloned, untrusted content.

## Verification

First, structural — every document declares its kind:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/workflows/*.toml; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"workflow"' "$f" || echo "unconverted: $f"
done
```

Second, the real load. `plect workflow show` compiles a workflow's whole
plan, so it reports the first thing that will not load, and it prints each
`[display]` and node input back as its author wrote it — which is the check
that a conversion kept the wiring it meant to:

```bash
plect workflow list                       # every id that loaded
plect workflow show <id>                  # its display values, nodes, inputs
```

`plect workflow show` treats a workspace provider's details as best-effort
and swallows a provider load failure, so it is the right check for the
workflow and not for its provider. A load check for providers, with no side
effects, is `plect up "does-not-match://x" --workflow <id>`, which loads
every provider and stops at name resolution.

Third, resolution rather than loading — a value can load and still fail to
resolve, which is what an unstated `default` looks like:

```bash
plect ls                     # resolves every session's display values
plect up <session>           # resolves every node input that is not produced
```

A `resolved to nothing and declares neither default nor optional` error names
the input to fix. `plect ls` is the softer half: a display value that does not
resolve leaves the field at whatever the listing already shows rather than
failing, so a workflow whose `[display]` silently stops updating is a
projection to check by hand with `plect workflow show`.
