# Config declaration identity

## Context

This decision is specified by
[`docs/design/config-declaration-identity.md`](../design/config-declaration-identity.md).

Configuration kind has been inferred from directory placement:
`tasks/`, `workspaces/`, `channels/`, `workflows/`, and neighboring kind
directories. That made the loader simple, but it coupled responsibility to
layout.

The defects are visible in the shipped catalog. The Claude Code plugin's launch
task has been named `claude`, so the qualified reference reads
`official/claude/claude`. The repeated provider name does not state the task's
responsibility. Other plugins carry similar repetition or forced prefixes,
including `tmux`, `codex`, `claude_initial_prompt`, and
`codex_initial_prompt`.

The namespace has also been per kind. A task, channel, workflow, or workspace
provider can share an id in one mounted layer. That prevents references from
being clear addresses and lets names stay generic because the directory supplies
hidden context.

Finally, authors cannot organize declarations by feature. A plugin that wants
to keep the runtime task, delivery channel, and workflow example together must
split them across kind directories. A file read away from its path also does not
declare the contract it implements.

Terraform supplies the closest precedent for split-equals-concatenation:
configuration files are packaging, and the module's blocks are the unit of
meaning. Plecture uses the same principle for config definition blocks.

## Decision

Plecture TOML configuration definitions are declared by top-level definition
tables:

```toml
[runtime]
kind = "task"
```

The table name is the definition id. The `kind` field is required. The kind
values are `workspace`, `resource`, `environment`, `channel`, `task`, and
`workflow`. Ids come only from definition table names. Filenames and directory
names are organizational. Valid ids are TOML bare-key segments matching
`^[A-Za-z_][A-Za-z0-9_]*$`. Hyphens are not valid because task ids are also
workflow node ids when a node omits `id`, and node ids must remain safe in
dotted Go template fields.

The unit of meaning is the definition block. Splitting definitions across any
number of `.toml` files and concatenating those files into one TOML document in
path-sorted traversal order are semantically equivalent, except that
file-relative path fields stay relative to the file containing the definition
table. The loader walks a config layer's definition root recursively, parses
every TOML file in lexicographic order by slash-separated relative path, merges
top-level definition tables, appends array-of-table entries in that order, and
fails on duplicate definitions.

Each plugin has one definition id namespace across TOML definition kinds.
Same-id conflicts inside one plugin are load errors even when the kinds differ.
Different plugins may use the same id because catalog-qualified references
carry the catalog alias and plugin path. Each user-owned layer has one
definition id namespace across TOML definition kinds; same-id conflicts inside
one user-owned layer are load errors even when the kinds differ. User-owned
layers may replace or merge a shallower user-owned definition only when the id
and kind match; a same-id, different-kind definition is a load error.
Catalog-qualified references select catalog plugin definitions and are not
shadowed by same-id relative definitions in user-owned layers. Same-id
user-owned definitions do not extend plugin-owned workflows.

References are dotted addresses. The loadable stored forms are:

- relative: `<id>`;
- catalog-qualified: `<catalog-alias>.<plugin-path>.<id>`.

There is no alias-optional middle form in stored config. A user-owned layer that
references catalog content must include the catalog alias. A plugin-owned layer
uses only relative references because aliases are user-local and unknowable to
plugin authors, and cross-plugin references remain banned by the plugin boundary
rules. A user-owned relative reference that would select catalog content is a
load error.

Catalog aliases and plugin path segments use the same lexical rule as
definition ids, `^[A-Za-z_][A-Za-z0-9_]*$`, because dots separate address
segments. A catalog-qualified reference selects the longest enabled plugin path
under the named catalog alias; the remaining final segment is the definition id.

Reference sites declare their expected kind. A workflow node `uses` field
expects `task`, workflow event channel bindings expect `channel`,
`workspace_provider` expects `workspace`, and task chain workflow references
expect `workflow`. A reference carries no kind segment. After resolving the
reference, the loader validates that the target definition's `kind` matches the
site's expected kind. A mismatch is a load error naming the site, the reference,
the expected kind, and the target's declared kind.

Exemplar workflow designs may write alias-less plugin-qualified references such
as `claude.runtime` as scaffold input. Scaffolding rewrites that form to the
user's catalog alias during copy-time verification before storing the workflow
as config.

Task-nesting and chain-spawn inner references use the same dotted grammar.
If a workflow node omits `id`, its id defaults to the referenced task
definition id rather than the full dotted reference.

Executable lookup through `{{bin}}` remains a separate slash-based namespace.
It resolves catalog plugin identities and executable names, not TOML definition
blocks, and it must split arbitrary-depth plugin paths from executable names.

The workspace-dir `.plect/` overlay keeps the existing trust boundary.
Recursive free-layout discovery applies to trusted roots. The cloned
workspace-dir overlay loads only workflow fragments from `.plect/workflows/`,
rejects task definitions, and does not load workspace, resource, environment, or
channel definitions. Workflow fragment identity still comes from a top-level
definition table with `kind = "workflow"`; the directory is only an allowlist.

Shipped plugin definition ids are renamed to responsibility names as part of the
migration:

| Plugin | Old id | Kind | New id |
|---|---|---|---|
| `tmux` | `tmux` | task | `terminal` |
| `claude` | `claude` | task | `runtime` |
| `claude` | `claude_initial_prompt` | task | `initial_prompt` |
| `claude` | `claude` | channel | `structured_delivery` |
| `codex` | `codex` | task | `tui_runtime` |
| `codex` | `codex_exec` | task | `exec_runtime` |
| `codex` | `codex_initial_prompt` | task | `initial_prompt` |
| `codex` | `codex_exec` | channel | `exec_delivery` |
| `github` | `github` | workspace | `worktree` |
| `github` | `github` | resource | `issue_pr` |
| `slack` | `slack` | channel | `thread_messages` |
| `okf` | `local-okf` | workspace | `concept_workspace` |
| `okf` | `okf_goal` | resource | `goal` |
| `okf` | `goal_review` | workflow | `goal_review_session` |
| `okf` | `goal_review` | task | `record_goal_verdict` |
| `okf` | `goal_bootstrap` | task | `bootstrap_goals` |

The `github` tasks `work`, `review`, `respond`, `investigate`, and `gh_guard`
already name responsibilities and keep their ids. The `codex` channel
`terminal_submit` already names its responsibility and keeps its id. The `okf`
task `pursue_goal` already names its responsibility and keeps its id.

JSON Schema expression for definitions uses `if`/`then` with `const` checks, or
a discriminator over the `kind` field. Naive `oneOf` output can produce noisy
errors for the wrong variants, but that is a managed schema-authoring cost, not
a deciding argument for or against the field form.

## Consequences

This is a breaking configuration change. Plecture is pre-1.0, so the
implementation uses a one-time migration rather than compatibility shims that
read both placement-as-kind and table-declared formats.

Core loader code needs recursive definition scanning for trusted config roots,
definition-table validation, required-kind validation, per-plugin and
per-user-layer namespace conflict detection, dotted reference parsing,
alias-form validation, reference-site kind validation, and preservation of the
restricted workspace-dir overlay.

Config authors can organize by feature, by kind, or flat layout. A moved file
keeps its definition identity because the top-level table name, not the
filename, declares the id.

Nested addresses are one level shallower than the header form: schema tables
use `[runtime.outputs_schema]`, not `[task.runtime.outputs_schema]`.
Definition enumeration and tooling can treat every definition as the same
top-level table shape and dispatch by the required `kind` field.

User-owned same-id definitions no longer shadow plugin definitions or append to
plugin workflows. Operators who used same-id user config to replace a plugin
task or partially extend a plugin workflow must make that customization
explicit: define a user-owned replacement workflow or task, update user-owned
entrypoints and references to the relative address, and use catalog-qualified
references for any plugin definitions the replacement still composes.

The migration inserts definition tables and required `kind` fields into shipped
plugin declarations, renames shipped responsibility ids, nests existing
declaration fields under their definition tables, and updates references in
workflows, tasks, README examples, migration docs, and tests that name those
ids. It also detects same-id user-owned definitions that previously shadowed or
extended plugin definitions so operators can convert them to explicit
replacements.

## Alternatives considered

### Keep placement-as-kind

Placement-as-kind was kept while the config tree was mostly single-purpose
directories, and the
[`plugin config layout migration`](../migrations/plugin-config-layout-migration.md)
moved those per-kind directories under plugin `config/` as mounted layer roots.
It loses now because the tree is a wiring language: placement hides kind from
the file, blocks feature-oriented layout, and lets weak ids rely on directory
context instead of naming responsibility.

### Header form `[<kind>.<id>]`

The header form was proposed because it made kind and id appear together in the
table address. Review dismantled its three claimed advantages.

First, forgetting the `task.` prefix is the same class of author mistake as
omitting the `kind` field, only relocated from a field to the table name. Both
forms are caught at load time. The field form gives the clearer diagnostic:
`kind` is missing. The header form can instead report an unknown kind using the
author's id as the apparent kind.

Second, TOML rejects duplicate `[id]` headers and duplicate `[kind.id]` headers
inside one file equally, so the parser-level intra-file collision argument does
not distinguish the forms. Cross-file collision detection remains loader work
in either design.

Third, loader dispatch is trivial in both forms. The header form dispatches from
the first table segment; the field form dispatches from a required scalar.

The field form keeps advantages the header form does not: nested tables are one
level shallower, every definition has the homogeneous `[id]` table shape,
missing-kind diagnostics are direct, and the manifest style is familiar from
Kubernetes resources that declare kind in the object body.

### Separate top-level `id` field

Keeping both `kind` and `id` as scalar fields would make a file
self-describing, but it would make the TOML table name organizational again.
The definition id must live in the top-level table name so TOML duplicate-header
checks and cross-file table merging operate on the same unit the language
references.

### Slash-separated config references

Slash-separated config references such as `official/claude/runtime` preserve
the old plugin identity style, but they make TOML definitions and TOML
references use different grammars. Dotted references are the definition table
address with ownership segments prepended.

### Kind segment in config references

Kind-segment references such as `official.claude.task.runtime` were proposed as
redundant validation. They lose because the reference-bearing key is already
the type annotation: workflow node `uses` expects a task, event channel bindings
expect a channel, and `workspace_provider` expects a workspace. The machine
still validates kind by resolving the reference and comparing the target's
declared `kind` to the site's expected kind, so the kind segment adds reader
and parser noise without adding a distinct check.

### Optional alias when unambiguous

Allowing `claude.runtime` in stored user config when only one catalog contains
`claude` is action-at-a-distance fragile: enabling a second catalog can
invalidate a reference that never mentioned it. Mandatory aliases make catalog
dependencies grep-able and make validity depend on the reference itself.

### Reference-site inference without declared-kind cross-check

Using the reference site to infer kind but not validating the resolved target's
declared kind would make bad config load until a later execution path failed or
used the wrong shape. The site remains the source of the expected kind, and the
target's required `kind` field is the cross-check.

### Keep per-kind namespaces

Per-kind namespaces preserve today's collision model, but they keep ids
ambiguous until the reader also knows the reference site. They also fail to push
authors toward responsibility names. A single namespace makes collisions visible
at load time and makes `official.claude.runtime` meaningful without guessing
from context.
