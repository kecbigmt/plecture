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
referenced effect, channel, workspace provider, resource observer, and task
document. The metadata also names placeholders that must be replaced at scaffold
time because they are team-local policy rather than catalog-owned config.

```text
catalog.toml
okf/
tmux/
codex/
slack/
exemplars/
  workflows/
    okf-goal-review/
      exemplar.toml
      workflow.toml
```

`exemplars/` is a reserved catalog-root directory. Exemplar workflow packages
are explicitly enumerated by `catalog.toml`; directory presence alone does not
publish an exemplar.

```toml
schema_version = 1

plugins = [
  "okf",
  "tmux",
  "codex",
  "slack",
]

workflow_exemplars = [
  "okf-goal-review",
]
```

The exemplar id is the listed path under `exemplars/workflows/`, such as
`okf-goal-review`. The source identity used by commands is
`<catalog-alias>/<exemplar-id>`, such as `official/okf-goal-review`.

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
name = "OKF goal review"
description = "Dispatch an agent session against a local OKF goal resource, then record a review verdict."

[[references]]
kind = "workspace_provider"
id = "local_okf"
plugin = "okf"

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
| `kind` | yes | One of `workspace_provider`, `resource_observer`, `effect`, `task`, or `channel`. |
| `id` | yes | The id referenced by the workflow template. |
| `plugin` | yes | Catalog-relative plugin path expected to provide the id. |

Input-reference fields:

| Field | Required | Meaning |
|---|---:|---|
| `input` | yes | Workflow input name whose enum contains the referenced id. |
| `kind` | yes | One of `workspace_provider`, `resource_observer`, `effect`, `task`, or `channel`. |
| `id` | yes | The id accepted as a literal workflow input value. |
| `plugin` | yes | Catalog-relative plugin path expected to provide the id. |

Placeholder fields:

| Field | Required | Meaning |
|---|---:|---|
| `kind` | yes | One of `workspace_provider`, `resource_observer`, `effect`, `task`, or `channel`. |
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
- every `[[input_references]]` entry names a value present in
  `inputs_schema.properties.<input>.enum`;
- every `[[placeholders]]` entry with `input` names a value present in
  `inputs_schema.properties.<input>.enum`;
- every `[[references]]` and `[[input_references]]` entry names a plugin path
  listed by the same catalog's `catalog.toml`;
- every `[[placeholders]]` entry has a non-empty description;
- a node whose `uses` is an effect placeholder declares an explicit `id` matching
  the placeholder id;
- no `[[references]]` entry is unused by the workflow template;
- no `[[placeholders]]` entry is unused by either the workflow template or,
  when `input` is set, the named input enum;
- no `[[input_references]]` entry is unused by the named input enum.

## Scaffold verification

`plect workflow init` verifies the copy before writing the destination file.

For each `[[references]]` entry, plect checks that:

1. `<catalog-alias>/<plugin>` is enabled in `catalogs.toml`;
2. the enabled plugin provides the referenced id for the declared kind;
3. the referenced id resolves exactly as it will resolve from the destination
   workflow after the copy.

For each `[[input_references]]` entry, plect checks that the configured plugin
is enabled and provides the referenced id. These entries verify literal config
ids carried by workflow inputs rather than by workflow grammar positions.

If a plugin is not enabled, the error names the plugin to enable:

```text
exemplar official/okf-goal-review requires effect "exec_runtime" from plugin official/codex
```

If a placeholder remains unresolved, the error names the placeholder and its
description:

```text
exemplar official/okf-goal-review needs effect placeholder "slack_thread": provide a local effect that returns channel_id and thread_ts for the team conversation
```

Placeholder replacement is explicit. A caller supplies replacements on the
init command:

```bash
plect workflow init goal-review --from official/okf-goal-review \
  --replace task:goal_review=team_goal_review \
  --replace effect:envfile=team_envfile \
  --replace effect:slack_thread=team_slack_thread \
  --replace effect:initial_task=codex_initial_prompt
```

Each replacement must resolve in the destination workflow's config cascade
before the destination is written. Replacement does not rewrite arbitrary text:
it changes only ids in the workflow grammar position or input schema position
declared by the placeholder. For effect placeholders used by workflow nodes,
replacement changes the node's `uses` value and preserves the node `id`. For
task placeholders used by workflow input enums, replacement changes the matching
enum literal, default value, and example values for the declared input.

The OKF goal review workflow template declares placeholder node ids explicitly:

```toml
[goal_reviewer]
kind               = "workflow"
workspace_provider = "okf.local_okf"

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
`slack_thread` and changes only `uses`:

```toml
[[goal_reviewer.nodes]]
id   = "slack_thread"
uses = "team_slack_thread"
```

## OKF goal review exemplar

The OKF goal review composition is an exemplar workflow package. The OKF plugin
continues to own `local_okf`, `okf_goal`, and `goal_bootstrap`. Host-owned
`pursue_goal` and `goal_review` task documents supply the review policy around
those plugin-owned mechanics. The workflow that combines OKF with a terminal
multiplexer, an agent runtime, and team conversation delivery lives at:

```text
exemplars/workflows/okf-goal-review/
```

The exemplar template uses `local_okf` as its workspace provider, starts the
selected terminal and agent runtime effects, and wires event channels for
runtime delivery and team conversation delivery. `goal_review`, `slack_thread`,
and `initial_task` are scaffold-time placeholders unless the catalog later grows
plugin-owned provider-neutral declarations for those roles. `envfile` is also a
scaffold-time placeholder when the selected runtime expects a local effect that
supplies environment values.

The scaffolded user-owned copy may use any equivalent local declaration ids. The
exemplar metadata names the catalog defaults so missing plugin errors are
actionable, but the copied workflow is free to replace tmux, Codex, or Slack
with different providers after ownership transfers to the user.

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
