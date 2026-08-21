# Declarations, identity, and references

The language's unit of meaning is the definition block. Files are packaging.

Splitting definition blocks across any number of `.toml` files, and
concatenating those files into one TOML document in path-sorted traversal
order, are semantically equivalent — except for file-relative path fields,
which stay relative to the file containing the definition table.

## Definition blocks

A definition is a top-level table whose name is the definition id, carrying a
required `kind`:

```text
[<id>]
kind = "<kind>"
```

| Field | Meaning |
|---|---|
| `<id>` | The responsibility name references use. |
| `kind` | The declaration contract this block implements. |

The kind vocabulary is `task`, `work`, `channel`, `workflow`,
`workspace_provider`, and `resource_observer`.

Every kind is declared the same way, `work` included: a `[<id>]` table carrying
`kind`. What differs is the file a declaration lives in. A kind with a body is a
Markdown file whose `+++` TOML frontmatter holds that one declaration and whose
body belongs to it; a kind without a body is a TOML file. `work` is the one kind
with a body today, so a work declaration appears only in frontmatter, and a TOML
definition document carrying `kind = "work"` is a load error — the declaration
would have no instruction.

Nothing else changes for it: the id is the table name, the id grammar holds, the
namespace is shared, and references resolve the same way. See
[`work.md`](work.md).

A kind uses its bare concept name when the declaration's runtime counterpart
is its own instance: tasks instantiate into task instances, channels into
channel deliveries, workflows into workflow executions. A kind uses a role
compound when the declaration produces or observes something that exists apart
from the declaration: a `workspace_provider` produces workspaces, and a
`resource_observer` observes resources that exist externally. Every block in
this language is a definition, so no kind name says so.

An id is a TOML bare-key segment matching `^[A-Za-z_][A-Za-z0-9_]*$`. Quoted
keys are not ids. Dots are excluded because dots separate address segments.
Hyphens are excluded because a task id is also a workflow node id when a node
omits `id`, and a node id must be a safe dotted path segment.

An id names a responsibility, not a provider repeated for qualification: a
Claude Code plugin's launch task is `[runtime]`, so its catalog-qualified
address reads `official.claude.runtime`.

Nested and array tables stay under the definition table:

<!-- fixture: references/relative.toml -->
```toml
[worktree]
kind = "workspace_provider"

[worktree.setup]
type = "exec"
bin  = "github-worktree"
args = ["setup", "--resource", { from = "resource.id" }]

[runtime]
kind  = "task"
scope = "run"

[runtime.setup]
type = "exec"
bin  = "agent-runtime"
args = ["launch"]

[review_session]
kind               = "workflow"
workspace_provider = "worktree"

[[review_session.nodes]]
uses = "runtime"
```

Everything nested below a definition table belongs to that definition. A
definition does not exist without a top-level `[<id>]` table and its `kind`.

## Discovery

Each trusted config layer has a definition root: a plugin's `config/`
directory, the user config home excluding reserved root files, and a trusted
ancestor overlay's `.plect/` directory.

Within a definition root, every `.toml` file that is not a reserved root file,
and every `.md` file opening with `+++` frontmatter, is read recursively in
lexicographic order by slash-separated relative path. A `.md` file without that
frontmatter is a template asset, not a definition.
Subdirectories are author organization only: one definition per file and
kind-named directories such as `config/tasks/` are equally valid and mean the
same thing.

The reserved root files are `config.toml`, `catalogs.toml`, and `plect.lock`.
They are not definition files.

Cross-file array-of-table entries append in traversal order while preserving
in-file order. Cross-file duplicate definition ids in one layer are load
errors, as are table collisions below a definition table that TOML would
reject in one concatenated document.

The workspace-dir `.plect/` overlay is cloned, untrusted content and is not a
full definition root. It loads only workflow fragments, from
`.plect/workflows/`, under the workflow cascade rules; fragment identity comes
from the `[<id>]` table with `kind = "workflow"`, and the directory is only an
allowlist. Task definitions there are a load error, because cloned content
must not carry shell. Workspace provider, resource observer, and channel
definitions are not loaded at all.

## Namespaces

Each plugin has one definition id namespace across all kinds. Each user-owned
layer likewise has one. Two definitions with the same id in one layer are a
load error even when their kinds differ.

Different plugins may reuse an id; their full addresses differ by catalog
alias and plugin path.

| Deeper definition | Rule |
|---|---|
| Same id, same kind, whole-definition kinds | Replaces the shallower user-owned definition. |
| Same id, same kind, workflow | Merges with the shallower user-owned workflow by the workflow cascade rules. |
| Same id, different kind | Load error. |

The whole-definition kinds are workspace provider, resource observer, channel,
task, and work.

Catalog-qualified references select catalog plugin definitions and are not
shadowed by a same-id relative definition in a user-owned layer. A same-id
user-owned definition does not extend a plugin-owned workflow either: a
user-owned workflow or task selects a user-owned replacement by referencing
its relative address.

## References

References are dotted addresses:

```text
<id>
<catalog-alias>.<plugin-path>.<id>
```

Catalog aliases and plugin path segments match
`^[A-Za-z0-9][A-Za-z0-9_-]*$`. The first character is alphanumeric so a
segment cannot read as a CLI flag; hyphens are valid after it because aliases
and plugin paths never become node ids.

The relative form refers to a definition in the same plugin, or in the
user-owned layer stack. In user-owned config, a relative reference that would
select catalog content is a load error: the reference must carry its alias, so
that its validity depends on the reference and the alias it names rather than
on which other catalogs happen to be enabled. Stored config therefore has no
alias-optional middle form.

The catalog-qualified form's final segment is the definition id; the segments
between the alias and that id are the plugin path, which must name an enabled
plugin under that alias.

A reference written inside a plugin uses only the relative form: catalog
aliases are user-local and unknowable to a plugin author, and cross-plugin
references from shipped plugin config remain banned by the plugin boundary
rule.

<!-- fixture: references/qualified.toml -->
```toml
[my_review]
kind               = "workflow"
workspace_provider = "official.github.worktree"

[[my_review.nodes]]
uses = "official.tmux.pane"

[[my_review.nodes]]
id   = "agent"
uses = "official.claude.runtime"

[my_review.nodes.inputs]
tmux_session = { from = "nodes.pane.outputs.session_name" }

[[my_review.event.channel]]
name    = "runtime"
uses    = "official.claude.delivery"
include = ["plect.instruction", "user.emit"]

[my_review.event.channel.inputs]
path = { from = "nodes.agent.outputs.socket_path" }
```

Exemplar workflow designs may use the scaffold-only form `<plugin>.<id>` for a
single-segment plugin path. Scaffolding rewrites it to the user's alias during
copy-time verification, before the workflow is stored as config. An exemplar
for a multi-segment plugin path identifies its source plugin through scaffold
metadata instead of adding dotted segments.

### Expected kinds

A reference carries no kind segment. After resolution, the target's declared
`kind` must match the site's expected kind.

| Reference site | Expected kind |
|---|---|
| Workflow `workspace_provider` | `workspace_provider` |
| Workflow node `uses` | `task` |
| Workflow event channel `uses` | `channel` |
| Task `inner.uses` | `task` |
| Work chain `workflow` | `workflow` |
| Dynamic-instantiation target | `work` |
| Chain spawn target | `work` |
| `config.toml` `channels` | `channel` |

A mismatch names the site, the reference, the expected kind, and the target's
declared kind.

When a workflow node omits `id`, the node id defaults to the referenced
definition's id — not to the full dotted reference. For
`uses = "official.claude.runtime"` the default node id is `runtime`. Two nodes
that would default to the same id need an explicit `id`.

Nesting and chain-spawn references use this same grammar. They get no second
reference language.

Executable lookup is a separate namespace, not a definition reference: it
keeps the slash grammar described in
[`../design/plugin-packaging.md`](../design/plugin-packaging.md), because
selecting an executable must split an arbitrary-depth plugin path from an
executable name. See [`actions.md`](actions.md).

## Validation rules

- A discovered TOML file in a trusted definition root with no top-level
  definition table is a load error unless it is a reserved root file.
- A definition table missing `kind`, or carrying an unknown `kind`, is a load
  error.
- A work document opens with `+++` frontmatter holding exactly one declaration,
  whose kind is `work`.
- A TOML definition document declaring `kind = "work"` is a load error: a kind
  with a body is declared in frontmatter.
- A definition id must match `^[A-Za-z_][A-Za-z0-9_]*$`.
- Two definitions with the same id in one layer are a load error, whatever
  their kinds.
- A stored reference uses either the relative or the catalog-qualified form.
- A user-owned reference to catalog content includes the catalog alias.
- A plugin-owned reference includes no catalog alias and no other plugin's
  ownership segment.
- A reference site's expected kind matches the resolved target's `kind`.
- The workspace-dir `.plect/` overlay loads only the path-restricted workflow
  overlay.
