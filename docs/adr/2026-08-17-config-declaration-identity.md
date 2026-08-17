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

The owner-ratified correction is stronger than an in-file kind field. A field
can be omitted while leaving a file that looks like a definition. A section
header is the definition itself: no header, no definition. TOML also rejects
duplicate headers inside one file, so the parser covers intra-file definition
collisions and the loader only has to extend that rule across files.

Terraform supplies the closest precedent for split-equals-concatenation:
configuration files are packaging, and the module's blocks are the unit of
meaning. Plecture uses the same principle for config definition blocks.

## Decision

Plecture TOML configuration definitions are declared by section headers:
`[<kind>.<id>]`.

The kind values are `workspace`, `resource`, `environment`, `channel`, `task`,
and `workflow`. Ids come only from definition headers. Filenames and directory
names are organizational. Valid ids are TOML bare-key segments matching
`^[A-Za-z][A-Za-z0-9_-]*$`.

The unit of meaning is the definition block. Splitting definitions across any
number of `.toml` files and concatenating those files into one TOML document are
semantically equivalent, except that file-relative path fields stay relative to
the file containing the definition header. The loader walks a config layer's
definition root recursively, parses every TOML file, merges top-level kind
tables, and fails on duplicate definitions.

Each plugin has one definition id namespace across TOML definition kinds.
Same-id conflicts inside one plugin are load errors even when the kinds differ.
Different plugins may use the same id because catalog-qualified references carry
the catalog alias and plugin path. Each user-owned layer has one definition id
namespace across TOML definition kinds; same-id conflicts inside one user-owned
layer are load errors even when the kinds differ. User-owned layers may replace
or merge a shallower user-owned definition only when the id and kind match; a
same-id, different-kind definition is a load error. Catalog-qualified
references select catalog plugin definitions and are not shadowed by same-id
relative definitions in user-owned layers.

References are dotted addresses that extend the definition header outward. The
loadable stored forms are:

- relative: `<kind>.<id>`;
- catalog-qualified: `<catalog-alias>.<plugin-path>.<kind>.<id>`.

There is no alias-optional middle form in stored config. A user-owned layer that
references catalog content must include the catalog alias. A plugin-owned layer
uses only relative references because aliases are user-local and unknowable to
plugin authors, and cross-plugin references remain banned by the plugin boundary
rules. A user-owned relative reference that would select catalog content is a
load error.

The kind segment is mandatory even though the unified namespace makes it
informationally redundant. The reference site validates that the reference kind
is allowed for that field, and the resolved definition validates that its
declared kind matches the reference kind. A mismatch is a load error naming both
kinds. Reference-site kind inference is not used to fill in a missing segment.

Exemplar workflow designs may write alias-less plugin-qualified references such
as `claude.task.runtime` as scaffold input. Scaffolding rewrites that form to
the user's catalog alias during copy-time verification before storing the
workflow as config.

Task-nesting and chain-spawn inner references use the same dotted grammar.

The workspace-dir `.plect/` overlay keeps the existing trust boundary. Recursive
free-layout discovery applies to trusted roots. The cloned workspace-dir overlay
loads only workflow fragments from `.plect/workflows/`, rejects task
definitions, and does not load workspace, resource, environment, or channel
definitions.

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

## Consequences

This is a breaking configuration change. Plecture is pre-1.0, so the
implementation uses a one-time migration rather than compatibility shims that
read both placement-as-kind and header-declared formats.

Core loader code needs recursive definition scanning for trusted config roots,
definition-header validation, per-plugin and per-user-layer namespace conflict
detection, dotted reference parsing, alias-form validation, reference kind
validation, and preservation of the restricted workspace-dir overlay.

Config authors can organize by feature, by kind, or flat layout. A moved file
keeps its definition identity because the header, not the filename, declares the
id.

The migration inserts definition headers into shipped plugin declarations,
renames shipped responsibility ids, nests existing declaration fields under
their headers, and updates references in workflows, tasks, README examples,
migration docs, and tests that name those ids.

## Alternatives considered

### Keep placement-as-kind

Placement-as-kind was kept while the config tree was mostly single-purpose
directories, and the
[`plugin config layout migration`](../migrations/plugin-config-layout-migration.md)
moved those per-kind directories under plugin `config/` as mounted layer roots.
It loses now because the tree is a wiring language: placement hides kind from
the file, blocks feature-oriented layout, and lets weak ids rely on directory
context instead of naming responsibility.

### Top-level `kind` and `id` fields

Top-level identity fields make a file self-describing, but a field can be
forgotten while the rest of the file still looks like a definition. A header is
the definition. TOML parser duplicate-header errors also provide intra-file
collision detection for free.

### Slash-separated references

Slash-separated references such as `official/claude/task/runtime` preserve the
old plugin identity style, but they make definitions and references use
different grammars. Dotted references are the definition table address with
ownership segments prepended.

### Optional alias when unambiguous

Allowing `claude.task.runtime` in stored user config when only one catalog
contains `claude` is action-at-a-distance fragile: enabling a second catalog can
invalidate a reference that never mentioned it. Mandatory aliases make catalog
dependencies grep-able and make validity depend on the reference itself.

### Reference-site kind inference

A workflow node can only use a task, so the kind segment is redundant at that
site. Inference loses because it hides an important part of the address. Keeping
the segment converts redundancy into validation: the site, reference, and target
must agree on kind.

### Keep per-kind namespaces

Per-kind namespaces preserve today's collision model, but they keep ids
ambiguous until the reader also knows the reference site. They also fail to push
authors toward responsibility names. A single namespace makes collisions visible
at load time and makes `official.claude.task.runtime` meaningful without
guessing from context.
