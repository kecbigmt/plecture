# Exemplar workflows

This design is governed by
[`../adr/2026-08-17-exemplar-workflows.md`](../adr/2026-08-17-exemplar-workflows.md).

## Design Core

Running workflows are user-owned policy. A catalog can help a user start from a
known composition, but the catalog does not mount workflows into the runnable
configuration cascade. The copy is the ownership transfer: after scaffolding,
the destination workflow is ordinary user config and does not auto-update when
the catalog changes.

Catalogs ship workflow starters as exemplar workflow packages, not as
`config/workflows/*.toml` files. An exemplar package contains a workflow TOML
template plus metadata that names the catalog plugins expected to supply every
referenced effect, channel, and workspace provider. The metadata also names
placeholders that must be replaced at scaffold time because they are team-local
policy rather than catalog-owned config.

```text
catalog.toml
github/
tmux/
codex/
slack/
exemplars/
  workflows/
    review-starter/
      exemplar.toml
      workflow.toml
```

`exemplars/` is a reserved catalog-root directory. Exemplar workflow packages
are explicitly enumerated by `catalog.toml`; directory presence alone does not
publish an exemplar.

```toml
schema_version = 1

plugins = [
  "github",
  "tmux",
  "codex",
  "slack",
]

workflow_exemplars = [
  "review-starter",
]
```

The exemplar id is the listed path under `exemplars/workflows/`, such as
`review-starter`. The source identity used by commands is
`<catalog-alias>/<exemplar-id>`, such as `official/review-starter`.

`plect workflow exemplar list` lists exemplar packages from registered catalogs.
`plect workflow exemplar show <catalog-alias>/<exemplar-id>` shows the
metadata, expected plugins, placeholders, and destination preview. These
commands do not inspect unregistered catalogs.

`plect workflow init <name> --from <catalog-alias>/<exemplar-id>` copies the
exemplar's workflow template to:

```text
~/.config/plect/workflows/<name>.toml
```

`--workspace-dir <dir>` copies to:

```text
<dir>/.plect/workflows/<name>.toml
```

The command fails if the destination file already exists. The destination file
is plain workflow config; it contains no link back to the exemplar, no catalog
revision pin, and no update subscription.

## Exemplar metadata

`exemplar.toml` describes the package and the reference contract for
copy-time verification.

```toml
schema_version = 1
kind = "workflow"
workflow = "workflow.toml"
name = "Review starter"
description = "Dispatch an agent session against a GitHub worktree, then record a review verdict."

[[references]]
kind = "workspace_provider"
id = "worktree"
plugin = "github"

[[references]]
kind = "effect"
id = "pane"
plugin = "tmux"

[[references]]
kind = "effect"
id = "exec_runtime"
plugin = "codex"

[[references]]
kind = "channel"
id = "exec_delivery"
plugin = "codex"

[[references]]
kind = "channel"
id = "slack"
plugin = "slack"

[[placeholders]]
kind = "effect"
id = "envfile"
description = "Provide a local effect that returns environment values consumed by the selected runtime."

[[placeholders]]
kind = "task"
id = "goal_review"
input = "task"
description = "Provide the host-owned goal review task document accepted by the workflow input."

[[placeholders]]
kind = "effect"
id = "slack_thread"
description = "Provide a local effect that returns channel_id and thread_ts for the team conversation."

[[placeholders]]
kind = "effect"
id = "initial_task"
description = "Replace with the team's selected initial-instruction effect, such as a Codex or Claude starter effect."
```

Metadata fields:

| Field | Required | Meaning |
|---|---:|---|
| `schema_version` | yes | Exemplar metadata file-format version. Unknown values fail loud. |
| `kind` | yes | Must be `workflow` for entries under `exemplars/workflows/`. |
| `workflow` | yes | Relative path to the workflow template inside the exemplar package. |
| `name` | yes | Human-readable list and show label. |
| `description` | no | Display-only summary. |

Reference fields:

| Field | Required | Meaning |
|---|---:|---|
| `kind` | yes | One of `workspace_provider`, `effect`, or `channel`. |
| `id` | yes | Definition id referenced by the workflow template, or the final id segment of a scaffold-only catalog reference. |
| `plugin` | yes | Catalog-relative plugin path expected to provide the id. |

Placeholder fields:

| Field | Required | Meaning |
|---|---:|---|
| `kind` | yes | One of `workspace_provider`, `effect`, `task`, or `channel`. |
| `id` | yes | The id present in the template before replacement. |
| `description` | yes | Human-facing guidance for the local replacement. |
| `input` | no | Workflow input name whose enum contains this placeholder id. |

An exemplar package is valid when:

- `exemplar.toml` exists and has `schema_version = 1`;
- `kind = "workflow"`;
- `workflow` resolves inside the exemplar package after symlink resolution;
- the workflow template parses as a workflow file;
- every workflow-level workspace provider reference, every node effect `uses`,
  and every event channel `uses` is declared in either `[[references]]` or
  `[[placeholders]]`;
- every catalog-owned reference in the workflow template uses the scaffold-only
  `<plugin>.<id>` form when the source plugin path has one segment;
- every catalog-owned reference in the workflow template uses the bare `<id>`
  form when the source plugin path has more than one segment, with the source
  plugin identified by exactly one matching `[[references]]` entry;
- every `[[placeholders]]` entry with `input` names a value present in
  `inputs_schema.properties.<input>.enum`;
- every `[[placeholders]]` entry with `kind = "task"` has `input` set;
- every `[[references]]` entry names a plugin path listed by the same catalog's
  `catalog.toml`;
- every `[[placeholders]]` entry has a non-empty description;
- a node whose `uses` is an effect placeholder declares an explicit `id` matching
  the placeholder id;
- no `[[references]]` entry is unused by the workflow template;
- no `[[placeholders]]` entry is unused by either the workflow template or,
  when `input` is set, the named input enum.

A `[[references]]` entry is used when a workflow reference has the same expected
kind and final id segment. For scaffold-only references, the leading plugin
segment also matches the entry's `plugin`; `github.worktree` therefore uses the
entry with `plugin = "github"` and `id = "worktree"`. For bare references, the
entry's `plugin` must have more than one path segment, and no other entry of the
same kind may claim the same bare id.

## Scaffold verification

`plect workflow init` verifies the copy before writing the destination file.

For each `[[references]]` entry, plect checks that:

1. `<catalog-alias>/<plugin>` is enabled in `catalogs.toml`;
2. the enabled plugin provides the referenced id for the declared kind;
3. the referenced id resolves exactly as it will resolve from the destination
   workflow after the copy.

Before the destination file is stored, plect rewrites scaffold-only catalog
references to the user's catalog alias. A template reference such as
`codex.exec_runtime`, declared by `kind = "effect"`, `id = "exec_runtime"`,
`plugin = "codex"`, becomes `official.codex.exec_runtime` when the exemplar is
copied from `official/review-starter`. A bare template reference that matches a
multi-segment `[[references]]` entry is rewritten through that metadata, so
`runtime`, declared by `plugin = "session/runtime"` and `id = "runtime"`,
becomes `official.session.runtime.runtime`. Other bare ids are not rewritten;
they remain relative user-owned references and must resolve from the destination
workflow.

If a plugin is not enabled, the error names the plugin to enable:

```text
exemplar official/review-starter requires effect "exec_runtime" from plugin official/codex
```

If a placeholder remains unresolved, the error names the placeholder and its
description:

```text
exemplar official/review-starter needs effect placeholder "slack_thread": provide a local effect that returns channel_id and thread_ts for the team conversation
```

Placeholder replacement is explicit. A caller supplies replacements on the
init command:

```bash
plect workflow init goal-review --from official/review-starter \
  --replace task:goal_review=team_goal_review \
  --replace effect:envfile=team_envfile \
  --replace effect:slack_thread=team_slack_thread \
  --replace effect:initial_task=codex.codex_initial_prompt
```

Each replacement must resolve in the destination workflow's config cascade
before the destination is written. Replacement does not rewrite arbitrary text:
it changes only ids in the workflow grammar position or input schema position
declared by the placeholder. For effect placeholders used by workflow nodes,
replacement changes the node's `uses` value and preserves the node `id`. For
task placeholders used by workflow input enums, replacement changes the matching
enum literal, default value, and example values for the declared input.

Replacement values use the stored config reference grammar plus the
scaffold-only catalog form. A bare value such as `team_goal_review` is a
relative user-owned reference and is written unchanged. A value such as
`codex.codex_initial_prompt` names the `codex` plugin from the exemplar's
source catalog and is written as `official.codex.codex_initial_prompt` when the
copy is made from the `official` catalog alias. A fully catalog-qualified value
such as `team.codex.codex_initial_prompt` is accepted only when it resolves from
the destination workflow and is written unchanged.

The exemplar workflow template declares placeholder node ids explicitly:

```toml
[goal_reviewer]
kind               = "workflow"
workspace_provider = "github.worktree"

[[goal_reviewer.nodes]]
id   = "slack_thread"
uses = "slack_thread"

[[goal_reviewer.event.channel]]
name = "slack"
uses = "slack.slack"

[goal_reviewer.event.channel.inputs]
channel_id = { from = "nodes.slack_thread.outputs.channel_id" }
thread_ts  = { from = "nodes.slack_thread.outputs.thread_ts" }
```

The `--replace effect:slack_thread=team_slack_thread` result keeps the node id as
`slack_thread` and changes only `uses`. Catalog-owned scaffold-only references
are also alias-qualified in the stored copy:

```toml
[goal_reviewer]
kind               = "workflow"
workspace_provider = "official.github.worktree"

[[goal_reviewer.nodes]]
id   = "slack_thread"
uses = "team_slack_thread"

[[goal_reviewer.event.channel]]
name = "slack"
uses = "official.slack.slack"
```

## OKF goal review boundary

The OKF plugin owns `local_okf`, `okf_goal`, and `goal_bootstrap`. Host-owned
`pursue_goal` and `goal_review` task documents supply the review policy around
those plugin-owned mechanics, and the host owns the `goal_review` workflow that
chooses the terminal, agent runtime, and team conversation delivery.

The official catalog does not publish an OKF goal-review exemplar package unless
`plugins/catalog.toml` lists one in `workflow_exemplars` and the corresponding
`exemplars/workflows/<id>/` package is committed. A historical runnable
`goal_review` workflow can be copied from git history as user-owned config, but
that historical file is not a catalog-owned exemplar package and is not mounted
from the OKF plugin.

## Capability plugin rule

A capability plugin must not ship a runnable workflow that references ids
outside that plugin. In catalog packages, plugin-owned runnable config lives
under `config/`; exemplar workflow packages live under `exemplars/workflows/`
and are never mounted by `LoadWorkflows`.

The enforcement point is catalog validation. A plugin path containing
`config/workflows/*.toml` is invalid unless every referenced workspace provider,
resource observer, effect, task document, and channel id is provided by the same
plugin. Cross-plugin composition belongs in an exemplar package or in user-owned
workflow config.

## Non-goals

Mounted workflow plugins are not part of this design. A workflow that composes
several plugins is policy, not a capability surface.

Auto-updating composition packs are not part of this design. Teams that want
standard workflows distribute them through their own user-config repository or
configuration-management overlay.

Diff-against-exemplar is a possible convenience command, not part of the
initial scaffold contract. The user-owned workflow remains authoritative even
when such a comparison exists.
