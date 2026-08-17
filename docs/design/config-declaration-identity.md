# Config declaration identity

This design is governed by
[`../adr/2026-08-17-config-declaration-identity.md`](../adr/2026-08-17-config-declaration-identity.md).

## Scope

This document specifies TOML configuration definitions loaded from plugin and
user-owned config layers.

The language's unit of meaning is the definition block. Files are packaging.
Splitting definition blocks across any number of `.toml` files and
concatenating those files into one TOML document are semantically equivalent
except for file-relative path fields, which stay relative to the file that
contains the definition header.

Markdown templates remain template assets and are loaded by the template layer
described in
[`plugin-packaging.md`](plugin-packaging.md). A template asset is not a TOML
definition block.

## Definition Headers

A TOML configuration definition is declared by a section header:

```toml
[task.runtime]
scope = "run"
setup = "{{bin \"agent-runtime\"}} launch"
```

The header has two segments:

```text
[<kind>.<id>]
```

Header segments:

| Segment | Meaning |
|---|---|
| `<kind>` | The declaration contract this block implements. |
| `<id>` | The responsibility name used by references. |

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
value is a reference whose kind segment is `workspace`.

`id` is a TOML bare-key segment matching this regular expression:

```text
^[A-Za-z][A-Za-z0-9_-]*$
```

Quoted keys are not valid definition ids. Dots are not valid inside ids because
dots separate address segments. Hyphens are valid so existing responsibility
names such as `local-okf` can migrate without a forced spelling change.

Nested tables stay under the definition header:

```toml
[task.runtime.outputs_schema]
type = "object"
required = ["workspace_dir"]

[task.runtime.outputs_schema.properties.workspace_dir]
type = "string"
```

Array tables use the same containment:

```toml
[[workflow.review_session.nodes]]
uses = "task.runtime"
```

A definition does not exist without a `[<kind>.<id>]` header. TOML rejects a
duplicate table header inside one file; the loader applies the same duplicate
detection across files in a layer.

`id` is a responsibility name, not a provider name repeated for qualification.
For example, a Claude Code plugin task that launches the runtime uses
`[task.runtime]`, so a catalog-qualified reference reads
`official.claude.task.runtime`.

## Discovery and Concatenation

Trusted config layers have definition roots:

| Layer | Definition root |
|---|---|
| Plugin layer | The plugin's `config/` directory |
| Global user layer | The user config home, excluding reserved root files |
| Trusted ancestor overlay | The overlay's `.plect/` directory |

Within a definition root, plect recursively reads TOML files that are not
reserved root files. Subdirectories are author organization only. A shipped
plugin may keep one definition per file or may keep kind-named directories such
as `config/tasks/`; neither convention changes semantics.

Reserved root files are not definition files:

```text
config.toml
catalogs.toml
plect.lock
```

For each layer, the loader parses every discovered `.toml` file and merges the
top-level kind tables. The result is the same as parsing one concatenated TOML
document with those definition blocks. Cross-file duplicate definition ids in
the same layer are load errors. Cross-file table collisions below a definition
header are load errors unless TOML would accept the same shape in one
concatenated document.

Fields that name a file path relative to config, such as `*_schema_file`, are
resolved against the file that contains the owning `[<kind>.<id>]` definition
header.

The workspace-dir `.plect/` overlay is not a full definition root because it is
cloned, untrusted content. It keeps the plugin-packaging trust restrictions:

| Workspace-dir content | Behavior |
|---|---|
| Workflow fragments | Loaded from `.plect/workflows/` under the workflow cascade rules. |
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
alias and plugin path, such as `official.claude.task.initial_prompt` and
`official.codex.task.initial_prompt`.

Each user-owned layer has one definition id namespace across all TOML definition
kinds. Two definitions in the same user-owned layer with the same id are a load
error, even when their kinds differ.

User-owned layers are deeper than other user-owned layers. Catalog-qualified
references select catalog plugin definitions and are not shadowed by same-id
relative definitions in user-owned layers. A user-owned workflow or task uses a
user-owned replacement by referencing its relative address.

| Deeper definition | Rule |
|---|---|
| Same id, same kind, whole-definition kinds | Replaces the shallower user-owned definition. |
| Same id, same kind, workflow | Merges with the shallower user-owned workflow by the workflow cascade rules. |
| Same id, different kind | Load error. |

Whole-definition kinds are workspace, resource, environment, channel, and task.

## Reference Grammar

References are dotted addresses that extend the definition header outward:

```text
<kind>.<id>
<catalog-alias>.<plugin-path>.<kind>.<id>
```

The relative form, `<kind>.<id>`, refers to a definition in the same plugin or
in the user-owned layer stack. In user-owned config, a relative reference that
would select catalog content is a load error; the reference must use the
catalog-qualified form.

The catalog-qualified form,
`<catalog-alias>.<plugin-path>.<kind>.<id>`, refers to a definition shipped by a
catalog plugin. A plugin path with multiple catalog path segments uses those
segments in order before the kind segment.

Stored config has no alias-optional middle form. A user-owned layer that
references catalog content uses the catalog-qualified form, so the reference's
validity depends on the reference itself and the configured alias it names, not
on which other catalogs happen to be enabled.

A reference written inside a plugin uses only the relative form. Catalog aliases
are user-local and unknowable to plugin authors. Cross-plugin references from
shipped plugin config remain banned by the plugin boundary rule.

Exemplar workflow designs may use the scaffold-only form
`<plugin>.<kind>.<id>`, such as `claude.task.runtime`. Scaffolding rewrites that
form to the user's catalog alias during copy-time verification before storing
the workflow as config.

The kind segment is mandatory even though the unified namespace makes it
redundant for lookup. The loader uses that redundancy as validation:

| Reference site | Required reference kind |
|---|---|
| `workflow.<id>.workspace_provider` | `workspace` |
| `workflow.<id>.nodes[].uses` | `task` |
| `workflow.<id>.event.channel[].uses` | `channel` |
| `task.<id>.chains[].workflow` | `workflow` |

A reference whose kind segment does not match the kind required by the site is
a load error. A reference whose kind segment does not match the target
definition's declared kind is also a load error naming both kinds.

Inner references used by task-nesting and chain-spawn designs use the same
dotted grammar. They do not get a second reference language.

## Worked Example

This plugin organizes by feature. Tasks, channels, and a workflow live together
under one directory:

```text
plugin.toml
config/review/runtime.toml
config/review/delivery.toml
config/review/session.toml
```

`config/review/runtime.toml`:

```toml
[task.runtime]
scope = "run"
setup = "{{bin \"agent-runtime\"}} launch"
```

`config/review/delivery.toml`:

```toml
[channel.runtime_events]
type = "exec"
command = "agent-runtime-send"
args = ["{{.Event.type}}", "{{.Event.body}}"]
```

`config/review/session.toml`:

```toml
[workflow.review_session]
workspace_provider = "workspace.worktree"

[[workflow.review_session.nodes]]
uses = "task.runtime"

[[workflow.review_session.event.channel]]
name = "runtime"
uses = "channel.runtime_events"
```

The three definitions load correctly because their ids are unique in the plugin
layer. The directory name `review/` has no semantic effect.

## Validation Rules

- A discovered TOML file in a trusted definition root with no definition header
  is a load error unless it is a reserved root file.
- A definition header whose kind is outside the kind vocabulary is a load error.
- Two definitions with the same id in one plugin are a load error, even when
  their kinds differ.
- Two definitions with the same id in one user-owned layer are a load error,
  even when their kinds differ.
- A definition id must match `^[A-Za-z][A-Za-z0-9_-]*$`.
- A reference must include a kind segment.
- A user-owned reference to catalog content must include the catalog alias.
- A plugin-owned reference must not include a catalog alias or another plugin's
  ownership segment.
- A reference site's required kind must match the reference's kind segment.
- A resolved target's declared kind must match the reference's kind segment.
- The workspace-dir `.plect/` overlay does not participate in recursive
  free-layout discovery; it loads only the path-restricted workflow overlay.
