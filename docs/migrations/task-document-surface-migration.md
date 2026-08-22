# Task document surface migration

The task document is the fifth runtime surface cut over to the ratified
configuration language (`docs/language/tasks.md`, `plecture.schema.json`). A
declaration that says what work is to be done and what would make it done is
now a **Markdown file** in `tasks/`: a `+++` TOML frontmatter block declaring
`kind = "task"`, and a body that is the instruction.

The two kinds share the `tasks/` root, because a directory name is not
semantic. What separates them is the serialization the language assigns each
kind: a kind with a body is a Markdown file, a kind without one is TOML. So
`tasks/*.toml` stays the effect surface, and `tasks/*.md` is the task
document surface.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own declarations, so run `plect plugin
update` once the catalog has migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Task instances hold state in the session store, so back that up too:

```bash
cp -r "${XDG_STATE_HOME:-$HOME/.local/state}/plect" \
      "${XDG_STATE_HOME:-$HOME/.local/state}/plect.migration-backup.$STAMP"
```

## Which declarations move

A declaration moves to a task document when it is *about work*: it declares
`done_when`, or `[[chains]]`, or its whole `setup` exists to render an
instruction. A declaration that brings something up and takes it down — a
worktree, a pane, an agent process — stays an effect.

A declaration that does both splits: the lifecycle half stays an effect that
a workflow node names, and the work half becomes a document that
`plect task setup` instantiates.

## Convert each declaration

Before — `tasks/review.toml`, an effect whose setup renders an instruction
and whose completion is a derived comparison:

```toml
[review]
kind     = "effect"
scope    = "session"
requires = ["resource_kind", "verdict_current"]

[review.setup]
type   = "shell"
script = '''
set -eu
INSTRUCTION=$(plect template render review --session "$session")
jq -nc --arg instruction "$INSTRUCTION" '{instruction:$instruction}'
'''

[review.setup.bind]
session = { from = "session.name" }

[[review.outputs]]
produces             = ["resource_kind", "revision"]
from_resource_status = true

[[review.outputs]]
name   = "verdict_current"
script = '''
CURRENT=$(plect resource status "{{.ResourceID}}" --json | jq -r '.state.revision')
if [ "{{.Self.verdict_revision}}" = "$CURRENT" ]; then echo true; else echo false; fi
'''

[review.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "verdict_current", in = ["true"] },
]

[review.done_when.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
```

After — `tasks/review.md`. The instruction is the body, the observed facts
are read from the root that publishes them, and the derived comparison is an
expression over the two live roots:

```markdown
+++
[review]
kind              = "task"
description       = "Review a resource and record a verdict"
resource_observer = "pull"

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
]

[review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review {{ resource.id }} and record a verdict against its current revision.
```

### The instruction is the body

A setup whose only job was to render a template and emit an `instruction`
output has nothing left to do: the body *is* the instruction, and
instantiation records it. Move the template asset's content below the closing
`+++` and delete both the setup and the `instruction` output.

The body keeps the conditional and defaulting forms the instruction assets
already had — `{{ if }}`, `{{ get }}`, and the `.SessionInputs` / `.Workflow`
vocabulary all still render. What is new beside them is the language's own
projection, `{{ <path> }}` over the roots
`docs/language/values.md` lists for the instruction body. The two spellings
do not collide: a language projection is a bare lowercase dotted path and
nothing else inside its delimiters.

### Every completion key names its root

A check leaf's key is now a rooted path, and each root is resolved at load
against the contract that declares it:

| Before | After | Declared by |
|---|---|---|
| `{ check = "checks_status", … }`, published by the observer | `{ check = "resource.state.checks_status", … }` | the declared observer's `state_schema` |
| `{ check = "verdict_revision", … }`, written in from outside | `{ check = "self.state.verdict_revision", … }` | this document's `state_schema` |

A key neither schema declares is a load error, so a misspelling fails before
anything runs. Nothing has to be re-listed to be readable: the two schemas
already say what exists.

### A derived output becomes an expression leaf

An `[[outputs]]` script whose whole job was to compare two facts has no
output of its own any more. State the comparison:

```toml
{ expr = "self.state.verdict_revision == resource.state.revision" }
```

An expression leaf that cannot be evaluated — because nothing has been
recorded yet — reads as pending, not as unsatisfied: "no verdict yet" is a
reason to wait, and only "they differ" is a reason to reopen.

### `resource_observer` is declared, and checked

A document states which observer it is written for. Two things follow, both
at load or instantiation rather than at run time: a `resource.state.*` key
the observer does not publish is a load error, and an instance bound to a
resource that observer does not claim is refused instead of created.

A document written for a resource kind with its own observer names that
observer. Where completion depends on *which* kind an instance got, two
documents state that better than one observer widened to cover both.

### `budget` is a sibling of `done_when`

```toml
[review.budget]
heartbeat_budget = 3
```

not `[review.done_when.budget]`. What it bounds is convergence, not one
predicate's shape. Omitting it leaves completion unbounded, which is what a
standing goal needs.

### The write path changed

`plect state set-output --task <instance>` wrote a mutable output. A task
document holds state instead, and the command for it is:

```bash
plect state set "$PLECT_SESSION_NAME" --instance review#1 \
  '{"verdict_revision":"a1b2c3d"}'
```

The payload is validated against the document's own `state_schema`, which is
also what says which keys exist — there is no `mutable = true` annotation to
add, because state is mutable by definition. Update every instruction,
runbook, and hook that recorded a fact through `set-output --task`.

An agent that used to read a value out of `plect status --json`'s `outputs`
now finds an observed fact under `observed.state` and a recorded one under
`state`. The observation carries the time it was taken, so a reader can see
how old it is.

## What has not moved yet

- **Five declarations still ship as effects with carried fields.** The
  loader still accepts `done_when`, `requires`, `[[outputs]]`, and
  `[[chains]]` on an effect declaration, and an unrooted completion key
  still resolves against the instance's own facts. Both go away with the
  last of those declarations; until then a converted document and a carried
  effect coexist in one config.
- **A document's `[[chains]]` are validated but not fired.** They are
  checked at load — the target workflow exists, and the inputs satisfy its
  contract — and a tick says out loud that they were not evaluated. The PR
  that moves the chain surface fires them.

## Rules that tightened

- A task document's frontmatter holds exactly one declaration, and its kind
  is `task`. A Markdown file declaring a bodiless kind is a load error.
- A lifecycle field is not part of the task grammar: `setup`, `cleanup`,
  `health`, `terminal`, and the nesting joint are an effect's, and declaring
  one on a document is a load error.
- A workflow node never references a task document. A node is a position in
  a lifecycle graph; work arrives afterwards, against the session that graph
  produced.
- Task ids share the one per-layer namespace every kind shares, so a
  document and an effect in the same layer cannot both be called `review`.
- Task documents are not loaded from the workspace directory: clone content
  must not declare the work it is about.

## Verification

First, structural — every task document declares its kind, and no
declaration kept a field that moved:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/tasks/*.md; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"task"' "$f" || echo "unconverted: $f"
  grep -q 'resource_observer' "$f" || echo "no observer declared: $f"
done
```

Second, the real load. `plect task show <id>` loads the document and runs its
contract checks, so it reports the first key, observer reference, or chain
input that will not resolve:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/tasks/*.md; do
  [ -e "$f" ] || continue
  plect task show "$(basename "$f" .md)" >/dev/null || echo "failed: $f"
done
```

Third, one instance end to end, against a real resource in a live session:

```bash
plect task setup review --resource <resource-url> --session <session>
plect status <session>            # observed … ago, and the pending predicate
plect state set <session> --instance review#1 '{"verdict_revision":"<sha>"}'
plect tick <session>              # observes, then decides: satisfied
```

If the last step does not report satisfied, `plect status <session> --json`
carries the whole predicate: each leaf, the value it read, and the root it
read it from.
