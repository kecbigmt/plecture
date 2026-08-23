# Task frontmatter migration

The `+++`-delimited TOML-frontmatter Markdown file —
`task-document-surface-migration.md`'s shape for a `kind = "task"`
declaration — is retired. A task is now an ordinary declaration in any
`tasks/*.toml` file, exactly like every other kind, discovered recursively
under `tasks/` the same way every other kind already is — a declaration
below a subdirectory is not a special case. Its instruction is its
`instructions` array: an ordered list of segments, each either inline `text`
or a sidecar Markdown file named by `file`, resolved relative to the
declaring TOML file and read as plain prose with no frontmatter of its own,
joined with a blank line in declaration order — see
[`../language/tasks.md`](../language/tasks.md).

The reason is legibility, not semantics: `+++` frontmatter in a `.md` file
renders as literal body text on GitHub, and TOML `#` comments inside it render
as Markdown headings. Nothing about the language's other constructs changed —
a task declaration still carries the same `resource_observer`, `done_when`,
`state_schema`, and `[[chains]]`; only the file it lives in and how the
instruction attaches to it changed.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own declarations, so run `plect plugin
update` once the catalog has migrated; plugins ship no task documents as of
this migration; a plugin still shipping one predates it.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Nothing in the session store changes shape — a task instance's persisted
state, its completion predicate, and every recorded verdict are untouched — so
there is no data migration beyond the config files themselves.

## Convert each document

For every `.md` file anywhere under `tasks/` whose first line is `+++` —
however deep it sits, and whatever its basename is; neither is significant to
the language — split it into a `.toml` declaration and a `.md` sidecar in the
same directory, keeping the original basename for both. Before —
`tasks/review.md`:

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

After — `tasks/review.toml` (the frontmatter, with `instructions` added) and
`tasks/review.md` (the body, unchanged prose, no frontmatter):

```toml
[review]
kind              = "task"
description       = "Review a resource and record a verdict"
resource_observer = "pull"
instructions      = [{ file = "review.md" }]

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
```

```markdown
Review {{ resource.id }} and record a verdict against its current revision.
```

This is mechanical: the frontmatter becomes the whole `.toml` file, the body
becomes the whole `.md` file, and `instructions = [{ file = "<basename>.md" }]`
is inserted anywhere among the declaration's bare `key = value` lines — it
must come before the first `[<id>.xxx]` sub-table, the same rule that already
governs every other field. Nothing in the body changes: `{{ <path> }}`
projections, and the transitional `{{ if }}` / `{{ get }}` forms carried over
from the old instruction assets, render exactly as before.

A body short enough to read comfortably inline may instead become a
`text = "..."` segment on the declaration (a TOML triple-quoted `"""..."""`
string for a multi-line body) with no sidecar file at all — `instructions`
accepts either segment shape, in any order, and mixed; which one a given
document uses is a style choice, not a semantic one.

The following shell function performs the mechanical half of the split for
one file (run from the directory holding `tasks/`), and finds every candidate
recursively rather than assuming `tasks/` is flat:

```bash
convert_task_document() {
  local f="$1"                        # e.g. tasks/agents/review.md
  local base="${f%.md}"
  local toml="$base.toml"
  awk '/^\+\+\+$/{c++; next} c==1{print}' "$f" > "$toml"
  awk '/^\+\+\+$/{c++; next} c>=2{print}' "$f" > "$f.new" && mv "$f.new" "$f"
  sed -i '/^kind *= *"task"/a instructions      = [{ file = "'"$(basename "$f")"'" }]' "$toml"
}

while IFS= read -r f; do
  head -c4 "$f" | grep -q '^+++$' && convert_task_document "$f"
done < <(find tasks -name '*.md')
```

Run it, then read every converted `.toml` file: the mechanical insertion
places `instructions` right after `kind = "task"`, which is always before any
sub-table, so no by-hand reordering is needed — but a document whose
frontmatter comment block sits above `[<id>]` keeps that comment block as
ordinary TOML comments, unmoved. `find tasks -name '*.md'` walks every
subdirectory, so a task document nested below `tasks/` converts the same way
one directly inside it does; the sidecar's `file` value is always just its
own basename, because `file` resolves relative to the declaring `.toml`
file's own directory, and the two sit side by side.

## Rules that changed

- A task declaration lives in a `tasks/*.toml` file, like every other kind —
  there is no longer a Markdown file class of its own.
- The instruction is the `instructions` array's elements, joined by a blank
  line. Each element carries exactly one of `text` and `file`; declaring both
  or neither on one element is a load error
  (`PLECTURE-CFG-TASK-INSTRUCTION-ELEMENT`).
- An element's `file` resolves relative to the declaring file and must stay
  within the same trusted layer; a path escaping it, even by way of a
  symlink, (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-CROSS-LAYER`) or naming no
  file (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING`) is a load error.
- A `.md` file is never itself a definition, regardless of its content. A
  `tasks/*.md` no instructions element's `file` names is a template asset,
  not a task — the repository's own orphan check
  (see `scripts/check-instruction-orphans.sh` if you maintain a similar check
  over your own config) is what flags one that looks like it was meant to be
  read.
- A TOML file declaring `kind = "task"` is no longer rejected — it is the only
  form now.
- Everything else about a task declaration — `resource_observer`,
  `state_schema`, `done_when`, `budget`, `[[chains]]`, the one per-layer id
  namespace, and the rule that a task document is not loaded from the
  workspace directory — is unchanged.

## Verification

Structural: no `.md` file under `tasks/` still opens with `+++`, and every
converted declaration carries an `instructions` array:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
find "$CONFIG_HOME/tasks" -name '*.md' | while IFS= read -r f; do
  head -c4 "$f" | grep -q '^+++$' && echo "unconverted: $f"
done
find "$CONFIG_HOME/tasks" -name '*.toml' | while IFS= read -r f; do
  grep -q 'kind *= *"task"' "$f" || continue
  grep -q 'instructions *=' "$f" || echo "no instructions: $f"
done
```

Then the real load. `plect task show <id>` loads the declaration and resolves
its instruction, so it reports a missing or cross-layer `file` immediately.
The id is the declaration's own `[<id>]` table name, not the filename — a
converted document (like any Plecture declaration) may name its table
anything, so read it out of the file rather than assuming it matches the
basename:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
find "$CONFIG_HOME/tasks" -name '*.toml' | while IFS= read -r f; do
  grep -q 'kind *= *"task"' "$f" || continue
  id=$(grep -oE '^\[[A-Za-z_][A-Za-z0-9_]*\]$' "$f" | head -1 | tr -d '[]')
  plect task show "$id" >/dev/null || echo "failed: $id ($f)"
done
```

This procedure was run against a copy of a real host configuration's seven
task documents, plus a synthetic task document nested one directory below
`tasks/` and declared under an id different from its filename, to exercise
both the recursive and the non-basename case; before this migration shipped,
all eight loaded, with their instructions resolved unchanged from their
sidecar files.

## Rollback

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
rm -rf "$CONFIG_HOME"
mv "$CONFIG_HOME.migration-backup.$STAMP" "$CONFIG_HOME"
```
