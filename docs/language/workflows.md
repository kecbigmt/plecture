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

# A chain's inputs are session inputs, so this schema is the contract they are
# checked against: the work facts the chain hands its reviewer are declared
# here, not passed around it.
[goal_reviewer.inputs_schema.properties]
work_session = { type = "string" }
instance     = { type = "string" }
judge_ids    = { type = "array", items = { type = "string" } }

[goal_reviewer.inputs_schema.properties.task]
type        = "string"
description = "Initial task to instantiate for this session."
enum        = ["goal_review", "none"]

[[goal_reviewer.nodes]]
uses = "pane"

[[goal_reviewer.nodes]]
id   = "worker"
uses = "official.codex.exec_runtime"

[[goal_reviewer.nodes]]
uses = "initial_task"

[goal_reviewer.nodes.inputs]
task      = { from = "session.inputs.task", default = "" }
queue_dir = { from = "nodes.worker.outputs.queue_dir" }

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

## Concurrency

`max_up_children` optionally caps how many sessions parented on a session
this workflow produces may hold run state "up" at once — for example, an
orchestrator workflow declaring `max_up_children = 7` limits itself to 7
concurrently-up child sessions. `plect up` rejects a child that would push
its parent past the cap, naming the parent, the cap, and the current count;
it does not queue the request, so the caller (typically an orchestrator
tick) retries on a later cycle once a child frees up.

Counting rule: a child counts toward the cap while it holds run state
"up" — any run-scoped task produced — and stops counting the moment it goes
down or is destroyed. A `plect up` in flight against the same parent, admitted
but not yet up itself, also counts, closing the window between two
concurrent `plect up` processes deciding at the same instant; this is why a
rejection's reported count can briefly exceed what `plect ls` shows. A
`plect up` that only brings an already-up child back up again (an idempotent
re-up) is exempt from the cap, since that child is already counted.

An in-flight admission survives however long its `plect up` process
legitimately keeps running — it counts until that process is confirmed
gone, not until some fixed time passes. If the process was killed instead
of finishing, `plect destroy` on that child clears the admission
immediately; a retried `plect up` on the same child also reclaims it.

Unset means no cap, the same behavior as today.

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
