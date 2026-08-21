# Workflows

A workflow is a named bundle of nodes plus the event channels, display values,
and clocks for the sessions it produces. It selects a workspace provider; the
provider owns the resource-kind knowledge, and the workflow owns the effect shape
on top of it. A workflow without one cannot acquire a workspace, so it cannot
back a session.

## Nodes

Each node selects an effect through `uses` and binds that effect's inputs. A node's
`id` defaults to the referenced definition's id.

The setup and cleanup graph is derived from the node bindings: a projection
rooted at `nodes.<id>.outputs` is a dependency edge. There is no `depends_on` —
wiring data is what declares order.

`blocks` declares the reverse edge, making each listed node a dependent of this
one. It exists for a cascade overlay that must insert itself ahead of base
nodes it cannot modify.

<!-- fixture: workflows/nodes.toml -->
```toml
[goal_reviewer]
kind               = "workflow"
name               = "goal-review agent (local-okf)"
description        = "Dispatch an agent session against a local-okf goal resource, then deliver the goal_review task's instructions."
workspace_provider = "okf_bundle"

[goal_reviewer.display]
title  = { from = "workflow.outputs.concept_id" }
status = "goal_review"

[goal_reviewer.inputs_schema]
type                 = "object"
required             = ["task"]
additionalProperties = false

[goal_reviewer.inputs_schema.properties.task]
type        = "string"
description = "Initial task to instantiate for this session."
enum        = ["goal_review", "none"]

[[goal_reviewer.nodes]]
uses = "pane"

[[goal_reviewer.nodes]]
id   = "worker"
uses = "official.codex.exec_runtime"

[goal_reviewer.nodes.inputs]
tmux_session = { from = "nodes.pane.outputs.session_name" }

[[goal_reviewer.nodes]]
uses = "initial_task"

[goal_reviewer.nodes.inputs]
task = { from = "session.inputs.task", default = "" }

[[goal_reviewer.event.channel]]
name    = "runtime"
uses    = "official.codex.exec_delivery"
include = ["plect.instruction", "user.emit", "plect.terminal.*"]

[goal_reviewer.event.channel.inputs]
queue_dir = { from = "nodes.worker.outputs.queue_dir" }

[goal_reviewer.tick]
on        = ["resource.*"]
heartbeat = "15m"
```

## Event channels

`[[<id>.event.channel]]` selects a channel definition instead of an effect and
adds an `include` allowlist of event-type globs. Its `inputs` are values over
the same roots node inputs use, evaluated at delivery.

`name` identifies the channel binding within the workflow; two bindings may
select the same channel definition under different names and includes.

## Display

`[<id>.display]` declares the values the CLI's listing and show commands, and
the web UI, render.
They read persisted outputs only — no network — so their freshness follows
whatever cadence updates those outputs.

## Clocks

`[<id>.tick]` declares when the tick reactor advances a session, on top of the
judge builtin trigger. `on` is a list of event-type globs whose match ticks the
session; `heartbeat` ticks when that long has passed since the last tick;
`max_heartbeat` caps the quiet-tick backoff interval. Both fields are optional
and independent: neither declared means a manual tick and the judge builtin
are the only drivers.

`[<id>.healthcheck]` declares the health sampling cycle: `period`,
`stall_threshold`, and `renotify_every`. It names the cycle, not what health
means; what each probe observes is an effect-level `[health]` declaration.

Unlike the rest of a workflow's fields, `[tick]` and `[healthcheck]` are
whole-table runtime tuning: a deeper cascade layer replaces a shallower layer's
table wholesale rather than merging into it.

## Provider parameters

`[<id>.workspace_provider_inputs]` sets the provider's author-declared
parameters. The values are literal data: the provider's hooks run before any
workspace or node output exists, so there is nothing for a projection to read.

## Validation rules

- `workspace_provider` and every node's `uses` resolve to a definition of the
  expected kind.
- Two nodes may not share an id, including after `id` defaulting.
- Dependencies derived from `nodes.<id>.outputs` projections form no cycle.
- A node input projecting `nodes.<id>.outputs.<key>` names a node in this
  workflow and an output that node's effect declares.
- `workspace_provider_inputs` values are literals.
- A cascade layer may add fields but not redeclare one a shallower layer set,
  except for the whole-table clocks.
