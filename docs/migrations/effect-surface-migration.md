# Effect surface migration

The effect is the fourth runtime surface cut over to the ratified
configuration language (`docs/language/effects.md`, `plecture.schema.json`).
A `tasks/*.toml` file is now a definition document: a `[<id>]` table
declaring `kind = "effect"`, whose `setup`, `cleanup`, `[health]` probes and
`[terminal]` verbs are **actions**, and whose nesting joint is `inner` plus
`[outputs.bind]`.

This surface is every session's lifecycle, so read the whole procedure before
running any of it. A pre-migration declaration fails to load with a
`PLECTURE-CFG-KIND-MISSING` diagnostic, and that failure reaches `plect
create`, `up`, `down`, `destroy`, `status`, `attach`, `capture`, and the tick
reactor alike — nothing half-runs.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own converted effects, so run
`plect plugin update` once the catalog has migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

## Convert each declaration

Before:

```toml
# tasks/envfile.toml
scope   = "session"
setup   = "touch .env && echo '{\"path\":\".env\"}'"
cleanup = "rm -f {{.Self.path}}"

[outputs_schema]
type     = "object"
required = ["path"]

[outputs_schema.properties]
path = { type = "string" }
```

After:

```toml
# tasks/envfile.toml — the filename is no longer the id
[envfile]
kind  = "effect"
scope = "session"

[envfile.setup]
type   = "shell"
script = "touch .env && jq -nc '{path:\".env\"}'"

[envfile.cleanup]
type   = "shell"
script = 'rm -f "$path"'

[envfile.cleanup.bind]
path = { from = "self.outputs.path", optional = true }

[envfile.outputs_schema]
type     = "object"
required = ["path"]

[envfile.outputs_schema.properties]
path = { type = "string" }
```

The mechanical rules the earlier surface migrations state apply here too: the
id is the table name, every field moves under it, `{{bin "name"}}` becomes
`bin = "name"` (or a `{ bin = "name" }` binding), and the schemas keep their
shape.

### Template variable to root

| Was | Becomes | Available in |
|---|---|---|
| `{{.SessionName}}` | `{ from = "session.name" }` | every action |
| `{{.ParentSession}}` | `{ from = "session.parent" }` | every action |
| `{{.SessionInputs.<key>}}` | `{ from = "session.inputs.<key>" }` | `setup` |
| `{{.WorkspaceDirPath}}` | `{ from = "workspace.dir" }` | `setup`, `cleanup`, `health` |
| `{{.Branch}}` | `{ from = "workspace.branch" }` | `setup`, `cleanup`, `health` |
| `{{.ResourceID}}` | `{ from = "resource.id" }` | `setup` only |
| `{{.Inputs.<key>}}` | `{ from = "inputs.<key>" }` | `setup`, `cleanup`, `health`, `inner.*` |
| `{{get .Inputs "<key>" ""}}` | `{ from = "inputs.<key>", default = "" }` | as above |
| `{{get .Prev "<key>" ""}}` | `{ from = "prev.<key>", default = "" }` | `setup` only |
| `{{.Self.<key>}}` | `{ from = "self.outputs.<key>" }` | `cleanup`, `health`, `terminal` |
| `{{.Nodes.<id>.outputs.<key>}}` | `{ from = "nodes.<id>.outputs.<key>" }` | `setup`, `cleanup`, `inner.*` |
| `{{.Workflow.outputs.<key>}}` | `{ from = "workflow.outputs.<key>" }` | `setup`, `cleanup`, `inner.*` |
| `{{.Locals.<key>}}` | `{ from = "locals.<key>" }` | `inner.*`, `outputs.bind` |
| `{{.Inner.outputs.<key>}}` | `{ from = "inner.outputs.<key>" }` | `outputs.bind` |
| `{{bin "<name>"}}` | `bin = "<name>"`, or `{ bin = "<name>" }` | every action |
| `{{terminal "<verb>"}}` | `{ terminal = "<verb>" }` | every action's bindings |

Each surface observes only its own roots, so a projection of the wrong one is
a load error rather than an empty value. Two consequences worth checking for
before you convert:

- **`cleanup` observes no `resource.id`.** An effect that has to release
  something resource-shaped records it as an output at setup, where the
  resource is observable, and its cleanup reads that output.
- **`cleanup` observes no `locals`.** A nesting layer's cleanup reads its own
  public contract, so a local that teardown needs is projected into it
  through `[outputs.bind]` and read back as `self.outputs.<key>`.

### Absence is now explicit

A projection whose source has nothing to report is a **load-time contract
statement, not an empty string**: it fails unless the value declares
`default` or `optional = true`. That matters most in `cleanup`, which runs
after a setup that may have failed before producing anything:

```toml
[runtime.cleanup]
type   = "shell"
script = '''
kill -TERM "$pid" 2>/dev/null || exit 0
[ -n "${hooks-}" ] && rm -f "$hooks"
true
'''

[runtime.cleanup.bind]
pid   = { from = "self.outputs.pid", optional = true }
hooks = { from = "self.outputs.hooks_settings", optional = true }
```

`optional = true` reaches the script as an **unset** shell variable, which
`${name-}` tells from an empty one. `default = ""` reaches it as an empty
value. Pick whichever the script already assumes; `{{get .Self "k" ""}}`
became `default = ""`, and a bare `{{.Self.k}}` a cleanup was relying on
rendering to nothing became `optional = true`.

### A shell action runs under `/bin/sh`

Plecture writes the script to a file and runs it by path through a generated
`#!/usr/bin/env sh` wrapper, where the old form was `bash -c <rendered
source>`. A bashism that used to work now fails at run time, not at load, so
convert them as you go:

| Bashism | POSIX form |
|---|---|
| `set -euo pipefail` | `set -eu` |
| `cmd <<<"$var"` | `printf '%s' "$var" \| cmd` |
| `${var//x/y}` | `printf '%s' "$var" \| tr -d x`, or `sed` |
| `[[ "$x" =~ re ]]` + `BASH_REMATCH` | `case "$x" in pattern) …` |
| `printf '%q'` | quote the value yourself, or restrict its charset |

`grep -nE 'set -[a-z]*o pipefail|<<<|\$\{[A-Za-z_]+[/#%]|\[\[|%q' <file>`
finds most of them.

### `shellQuote` is gone, and so is the hazard it patched

An `exec` action passes each value as one argv element, and a `shell`
action's values arrive through a private binding file — neither is command
text. Drop every `| shellQuote` as you convert.

A script that relays a value across a further boundary of its own still owns
the quoting at that boundary. The shipped agent launches do exactly that:
they type a command line into a terminal, so they quote what they type and
their `inputs_schema` keeps the charset restrictions that close that
boundary.

### `[health]` and `[terminal]` members are actions

```toml
[pane.health.alive]
type    = "exec"
command = "tmux"
args    = ["has-session", "-t", { from = "self.outputs.session_name" }]

[pane.terminal.send_text]
type    = "exec"
command = "tmux"
args    = ["send-keys", "-t", { from = "self.outputs.session_name" }, "--"]
```

A verb's operand — the text to type, the key token — arrives as the action's
first positional argument, so an exec verb ends with the flags before it and
a shell verb reads `"$1"`.

**A partial `[terminal]` table now loads.** Availability is per verb: an
effect may offer a capture and nothing else, and a value consuming a verb no
effect in the plan declares fails where it is consumed rather than at load.
Drop the always-succeeds stubs you added only to satisfy the old
all-or-nothing rule.

### The nesting joint

`inner` becomes a table naming what it wraps, and the three `[bind.*]` tables
are renamed to the surfaces they configure:

| Was | Becomes |
|---|---|
| `inner = "<ref>"` | `[<id>.inner]` with `uses = "<ref>"` |
| `[bind.inputs]` | `[<id>.inner.inputs]` |
| `[bind.env]` | `[<id>.inner.env]` |
| `[bind.outputs]` | `[<id>.outputs.bind]` |

Before:

```toml
# tasks/work.toml
inner = "official/github/work"

[bind.inputs]
instruction = '{{get .Inputs "instruction" ""}}'

[bind.outputs]
instruction   = "{{.Inner.outputs.instruction}}"
checks_status = "{{.Inner.outputs.checks_status}}"
```

After:

```toml
[work]
kind = "effect"

[work.inner]
uses = "official/github/work"

[work.inner.inputs]
instruction = { from = "inputs.instruction", default = "" }

[work.outputs.bind]
instruction   = { from = "inner.outputs.instruction" }
checks_status = { from = "inner.outputs.checks_status" }
```

A direct projection of an inner output keeps write-through for a mutable
output, exactly as before. A computation (`expr`) does not, and cannot be
declared `mutable` — but a computed binding is no longer required to declare
type `"string"`: it may declare whatever type its computation produces.

## What has not moved yet

Four fields stay where they are, and the loader still accepts them on an
effect declaration: `done_when`, `requires`, `[[outputs]]` (including
`from_resource_status`), and `[[chains]]`. They belong to a **task
document**, not to the effect surface, and the PR that introduces task
documents moves all four out. Leave them as they are, re-anchored under the
definition table like every other field:

```toml
[work]
kind     = "effect"
scope    = "session"
requires = ["checks_status"]

[[work.outputs]]
produces             = ["checks_status", "revision"]
from_resource_status = true

[work.done_when]
all = [ { check = "checks_status", in = ["SUCCESS", "NULL"] } ]

[[work.chains]]
id       = "review"
workflow = "codex"
```

Their contents are unchanged, and still Go templates: a `[[outputs]]` script
and a `[chains.inputs]` binding keep the `{{.Self.<key>}}` / `{{.Work.*}}`
vocabulary, because those surfaces have not moved. Convert only the fields
listed in the table above.

One shape is temporarily unavailable while they are carried: TOML admits one
`outputs` name per table, so a single declaration cannot have both
`[<id>.outputs.bind]` (the nesting joint) and `[[<id>.outputs]]` (a produced
output). A nesting layer that needs both has to wait for task documents; a
layer that needs only one is unaffected.

## Rules that tightened

- A field outside the effect surface is a load error
  (`PLECTURE-CFG-FIELD-UNKNOWN`), which is how a retired key such as
  `primary`, `idle_after`, or `execution` now reports itself. The surface is
  `scope`, `setup`, `cleanup`, `inner`, `outputs`, `health`, `terminal`,
  `locals_schema`, `inputs_schema`, `outputs_schema`, and their `_file`
  forms — plus, for now, the four carried fields above.
- `[health]`, `[terminal]`, `[inner]`, and `[outputs]` are closed tables: a
  misspelled probe, verb, or joint key is a load error rather than a
  declaration with no consumer.
- A shell action's `script` contains no interpolation
  (`PLECTURE-CFG-SHELL-INTERPOLATION`). Every value it needs is declared in
  its `bind` table.
- A definition id admits no hyphen (`^[A-Za-z_][A-Za-z0-9_]*$`), and the id
  is the table name rather than the filename. A file may hold several
  declarations, and a workflow node's `uses` names the id.

## Verification

First, structural — every converted document declares its kind:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/tasks/*.toml; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"effect"' "$f" || echo "unconverted: $f"
done
```

Second, the real load, without creating anything. `plect workflow show`
compiles a workflow's whole plan, so it loads every effect the workflow names
and reports the first that will not load. Run it for each workflow you have:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/workflows/*.toml; do
  [ -e "$f" ] || continue
  plect workflow show "$(basename "$f" .toml)" >/dev/null || echo "failed: $f"
done
```

Note what this does **not** cover: `plect workflow show` treats a workspace
provider's details as best-effort and swallows a provider load failure, so it
is the right check for effects and not for providers. For providers, use the
check in `workspace-provider-surface-migration.md`.

Third, resolution rather than loading — a value can load and still fail to
resolve, which is what an unstated `optional` looks like. Against an existing
session:

```bash
plect status <session>        # resolves the attach verb for display
plect capture <session>       # resolves and runs the capture verb
plect up <session>            # re-resolves every setup that is not produced
```

A `resolved to nothing and declares neither default nor optional` error names
the binding to fix.
