# Config declaration identity

This design is governed by
[`../adr/2026-08-17-config-declaration-identity.md`](../adr/2026-08-17-config-declaration-identity.md).

## Scope

This document specifies TOML configuration definitions loaded from plugin and
user-owned config layers.

The language's unit of meaning is the definition block. Files are packaging.
Splitting definition blocks across any number of `.toml` files and
concatenating those files into one TOML document in path-sorted traversal order
are semantically equivalent except for file-relative path fields, which stay
relative to the file that contains the definition table.

Markdown templates remain template assets and are loaded by the template layer
described in
[`plugin-packaging.md`](plugin-packaging.md). A template asset is not a TOML
definition block.

## Definition Tables

A TOML configuration definition is declared by a top-level table whose name is
the definition id:

```toml
[runtime]
kind = "task"
scope = "run"
setup = "{{bin \"agent-runtime\"}} launch"
```

The top-level table has this shape:

```text
[<id>]
kind = "<kind>"
```

Definition fields:

| Field | Meaning |
|---|---|
| `<id>` | The responsibility name used by references. |
| `kind` | The declaration contract this block implements. |

The `kind` field is required. A missing `kind` field is a load error. An
unknown `kind` value is a load error.

The `kind` vocabulary is:

| Kind | Declaration |
|---|---|
| `workspace` | Workspace provider |
| `resource` | Resource definition |
| `environment` | Execution environment |
| `channel` | Event channel |
| `task` | Task definition |
| `workflow` | Workflow definition |

`kind` uses the short code-facing config noun. The workflow field remains
`workspace_provider` because it names the role a workflow selects, following
[`workspace-provider-vocabulary.md`](workspace-provider-vocabulary.md); its
value is a reference whose site expects a `workspace` definition.

`id` is a TOML bare-key segment matching this regular expression:

```text
^[A-Za-z_][A-Za-z0-9_]*$
```

Quoted keys are not valid definition ids. Dots are not valid inside ids because
dots separate address segments. Hyphens are not valid because task ids are also
workflow node ids when `[[nodes]]` omits `id`, and node ids must be safe as
dotted Go template fields such as `.Nodes.runtime.outputs`.

Nested tables stay under the definition table:

```toml
[runtime.outputs_schema]
type = "object"
required = ["workspace_dir"]

[runtime.outputs_schema.properties.workspace_dir]
type = "string"
```

Array tables use the same containment:

```toml
[[review_session.nodes]]
uses = "runtime"
```

A definition does not exist without a top-level `[<id>]` table and its required
`kind` field. TOML rejects a duplicate table header inside one file; the loader
applies the same duplicate detection across files in a layer.

`id` is a responsibility name, not a provider name repeated for qualification.
For example, a Claude Code plugin task that launches the runtime uses
`[runtime]` with `kind = "task"`, so a catalog-qualified reference reads
`official.claude.runtime`.

## Discovery and Concatenation

Trusted config layers have definition roots:

| Layer | Definition root |
|---|---|
| Plugin layer | The plugin's `config/` directory |
| Global user layer | The user config home, excluding reserved root files |
| Trusted ancestor overlay | The overlay's `.plect/` directory |

Within a definition root, plect recursively reads TOML files that are not
reserved root files in lexicographic order by slash-separated relative path.
Subdirectories are author organization only. A shipped plugin may keep one
definition per file or may keep kind-named directories such as `config/tasks/`;
neither convention changes semantics.

Reserved root files are not definition files:

```text
config.toml
catalogs.toml
plect.lock
```

For each layer, the loader parses every discovered `.toml` file and merges the
top-level definition tables. The result is the same as parsing one concatenated
TOML document whose file sections appear in path-sorted traversal order.
Cross-file array-of-table entries append in that order while preserving in-file
order. Cross-file duplicate definition ids in the same layer are load errors.
Cross-file table collisions below a definition table are load errors unless
TOML would accept the same shape in one concatenated document.

Fields that name a file path relative to config, such as `*_schema_file`, are
resolved against the file that contains the owning `[<id>]` definition table.

The workspace-dir `.plect/` overlay is not a full definition root because it is
cloned, untrusted content. It keeps the plugin-packaging trust restrictions:

| Workspace-dir content | Behavior |
|---|---|
| Workflow fragments | Loaded from `.plect/workflows/` under the workflow cascade rules. Fragment identity comes from the `[<id>]` table with `kind = "workflow"`; the directory is only an allowlist. |
| Task definitions | Load error, because cloned content must not carry shell. |
| Workspace, resource, environment, or channel definitions | Not loaded. |

Recursive free-layout discovery applies to trusted roots. The untrusted
workspace-dir overlay stays path-restricted so cloned content cannot break a
session by adding an arbitrary TOML definition outside the workflow overlay.

## Per-Layer Namespace

Each plugin has one definition id namespace across all TOML definition kinds.
Two definitions in the same plugin with the same id are a load error, even when
their kinds differ.

Different plugins may use the same id. Their full addresses differ by catalog
alias and plugin path, such as `official.claude.initial_prompt` and
`official.codex.initial_prompt`.

Each user-owned layer has one definition id namespace across all TOML definition
kinds. Two definitions in the same user-owned layer with the same id are a load
error, even when their kinds differ.

User-owned layers are deeper than other user-owned layers. Catalog-qualified
references select catalog plugin definitions and are not shadowed by same-id
relative definitions in user-owned layers. Same-id user-owned definitions also
do not extend plugin-owned workflows. A user-owned workflow or task uses a
user-owned replacement by referencing its relative address.

| Deeper definition | Rule |
|---|---|
| Same id, same kind, whole-definition kinds | Replaces the shallower user-owned definition. |
| Same id, same kind, workflow | Merges with the shallower user-owned workflow by the workflow cascade rules. |
| Same id, different kind | Load error. |

Whole-definition kinds are workspace, resource, environment, channel, and task.

A user-owned replacement may intentionally reuse a plugin definition id, but it
is a separate user-owned definition. Plugin-owned relative references still
resolve inside the owning plugin. User-owned relative references resolve through
the user-owned layer stack. User-owned catalog-qualified references resolve to
the named plugin definition.

## Reference Grammar

References are dotted addresses:

```text
<id>
<catalog-alias>.<plugin-path>.<id>
```

Catalog aliases and plugin path segments are non-empty dot-free segments
matching `^[A-Za-z0-9_-]+$`. Dots are not valid inside aliases or plugin path
segments because dots separate address segments. Hyphens are valid because
aliases and plugin path segments never become workflow node ids.

The relative form, `<id>`, refers to a definition in the same plugin or in the
user-owned layer stack. In user-owned config, a relative reference that would
select catalog content is a load error; the reference must use the
catalog-qualified form.

The catalog-qualified form, `<catalog-alias>.<plugin-path>.<id>`, refers to a
definition shipped by a catalog plugin. A plugin path with multiple catalog path
segments uses those segments in order before the id segment. The parser selects
the longest enabled plugin path under the named catalog alias; the remaining
final segment is the definition id.

Stored config has no alias-optional middle form. A user-owned layer that
references catalog content uses the catalog-qualified form, so the reference's
validity depends on the reference itself and the configured alias it names, not
on which other catalogs happen to be enabled.

A reference written inside a plugin uses only the relative form. Catalog aliases
are user-local and unknowable to plugin authors. Cross-plugin references from
shipped plugin config remain banned by the plugin boundary rule.

Exemplar workflow designs may use the scaffold-only form `<plugin>.<id>`, such
as `claude.runtime`. Scaffolding rewrites that form to the user's catalog alias
during copy-time verification before storing the workflow as config.

Reference sites declare their expected kind:

| Reference site | Expected target kind |
|---|---|
| `workflow.workspace_provider` | `workspace` |
| `workflow.nodes[].uses` | `task` |
| `workflow.event.channel[].uses` | `channel` |
| `task.chains[].workflow` | `workflow` |

A reference carries no kind segment. After resolving the reference, the loader
validates that the target definition's `kind` matches the reference site's
expected kind. A mismatch is a load error naming the site, the reference, the
expected kind, and the target's declared kind.

If `workflow.nodes[]` omits `id`, the node id defaults to the referenced task
definition id, not to the full dotted reference. For
`uses = "official.claude.runtime"`, the default node id is `runtime`. Workflow
authors must set an explicit node id when two nodes would otherwise default to
the same id.

Inner references used by task-nesting and chain-spawn designs use the same
dotted grammar. They do not get a second reference language.

Executable lookup through `{{bin}}` is a separate executable namespace, not a
TOML definition reference. It keeps the slash-based plugin identity grammar
described in `plugin-packaging.md` because executable selection must split an
arbitrary-depth plugin path from an executable name.

## Worked Example

This plugin organizes by feature. A workspace provider, task, channel, and
workflow live together under one directory:

```text
plugin.toml
config/review/worktree.toml
config/review/runtime.toml
config/review/delivery.toml
config/review/session.toml
```

`config/review/worktree.toml`:

```toml
[worktree]
kind = "workspace"
root = "{{.Session.WorkspaceDirPath}}"
```

`config/review/runtime.toml`:

```toml
[runtime]
kind = "task"
scope = "run"
setup = "{{bin \"agent-runtime\"}} launch"
```

`config/review/delivery.toml`:

```toml
[runtime_events]
kind = "channel"
type = "exec"
command = "agent-runtime-send"
args = ["{{.Event.type}}", "{{.Event.body}}"]
```

`config/review/session.toml`:

```toml
[review_session]
kind = "workflow"
workspace_provider = "worktree"

[[review_session.nodes]]
uses = "runtime"

[[review_session.event.channel]]
name = "runtime"
uses = "runtime_events"
```

The four definitions load correctly because their ids are unique in the plugin
layer. The directory name `review/` has no semantic effect.

## Validation Rules

- A discovered TOML file in a trusted definition root with no top-level
  definition table is a load error unless it is a reserved root file.
- A definition table missing `kind` is a load error.
- A definition table whose `kind` is outside the kind vocabulary is a load
  error.
- Two definitions with the same id in one plugin are a load error, even when
  their kinds differ.
- Two definitions with the same id in one user-owned layer are a load error,
  even when their kinds differ.
- A definition id must match `^[A-Za-z_][A-Za-z0-9_]*$`.
- Catalog aliases and plugin path segments must match
  `^[A-Za-z0-9_-]+$`.
- A stored reference must use either the relative `<id>` form or the
  catalog-qualified `<catalog-alias>.<plugin-path>.<id>` form.
- A user-owned reference to catalog content must include the catalog alias.
- A plugin-owned reference must not include a catalog alias or another plugin's
  ownership segment.
- A reference site's expected kind must match the resolved target definition's
  `kind`.
- The workspace-dir `.plect/` overlay does not participate in recursive
  free-layout discovery; it loads only the path-restricted workflow overlay.
