# Workflows

A workflow assembles a session environment and declares exactly one entry
resource type. It does not declare the session's task. A task selected by the
caller may bind the entry resource or another concrete resource type.

## Session creation and identity

`plect up <resource>` resolves the concrete identifier to a resource type and
accepts `--workflow <id>` and optional `--tag <tag>`. The selected workflow's
`resource` must equal the resolved type. Without `--workflow`, exactly one
workflow accepting that type is required; zero or more than one is an error
that lists the candidates.

The resource's `name` derives its resource name. Without a tag, that value is
the session name. A tag appends one validated non-empty session-name segment,
`<resource-name>+<tag>` with the tag matching `[A-Za-z0-9_-]+`. The resulting
name must either be new or name the same entry resource and tag: a live
session with that name that refers to another resource, or that records
another tag, is a collision, not the same session. The session records a
reference to its entry resource, the tag, and the name composed from them at
creation; the identifier its resource actions receive belongs to the resource,
not the session. `session.tag` reads the recorded tag, so a resource name that
itself contains `+` never changes it. Tags
distinguish concurrent sessions for one resource without making a task,
plugin, workflow, or working-directory detail part of core identity.

The caller optionally selects an initial task and its concrete resource
binding. The task is compatible when its declared resource type matches that
binding. `plect up` may omit it, creating an environment a person may use.
`plect up --task <task>` binds the entry identifier unless `--task-resource`
names another concrete resource; an incompatible selection fails before the
session is created. The task becomes the session's `initial` instance.
Repeating the same selection keeps that instance, and a different selection
fails until the existing instance is cleaned.
Chains and populations use the same one-task selection surface, not a workflow
field. Adding or finishing a task never changes the entry resource, session
name, environment, or session lifetime.

## Nodes, work directory, and public outputs

Nodes are ordinary effects. A projection from `nodes.<id>.outputs.*` creates a
dependency edge; `blocks` states a reverse edge. A reference to
`workflow.outputs.*` expands to the producing node references in that public
output's binding, so a consumer depends on those producing nodes too. Public
outputs are evaluable as soon as their source node outputs exist; evaluation
adds no lifecycle node. The expanded edges participate in cycle detection
and in the explicit graph used to derive preparation nodes, before
default-workdir edges are added. A workflow has no provider node or special
lifecycle.

A node may declare `subscription = { from = "self.outputs.<key>" }` to bind
its session to the resource identifier produced at that output. This is a
single projection, separate from the node's setup inputs; it reads no other
node or value root. After setup produces the node, core stores a subscription
row with source `node`. After cleanup, core removes it when no entry resource,
task, or other live node still needs that identifier. Admission fails if the
resource's `max_subscribers` bound is reached. Omitting `subscription` leaves
the node's output available without binding delivery.

Changing only `subscription` in a workflow revision follows the ordinary
node-change classification: a session-scoped node requires recreation and a
run-scoped node requires a down/up restart.

`workdir`, when present, is exactly one projection from a node output and must
resolve to an absolute local filesystem directory path. Environments without a
local directory omit it:

```toml
[pull_review]
kind     = "workflow"
resource = "pull_request"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[pull_review.nodes]]
id   = "checkout"
uses = "checkout_effect"

[[pull_review.nodes]]
id   = "agent"
uses = "agent_runtime"

[[pull_review.nodes]]
id   = "orchestrator"
uses = "orchestrator_runtime"

[pull_review.nodes.inputs]
workspace_dir = { from = "nodes.checkout.outputs.workspace_dir" }
```

The workdir producer and all its transitive prerequisites are preparation
nodes. The graph determines that set before default-workdir dependencies are
added. Preparation setup actions run in the current user's home directory,
whichever way the session was created, so they never depend on the directory
they are creating or on a daemon's incidental cwd. Every other node's setup
and liveness actions run in the declared directory and depend on its producer.
If `workdir` is omitted, no node receives a default directory. There are no
per-node or per-action cwd overrides.

Cleanup uses the directory chosen for its node setup. If that directory has
vanished, cleanup is unavailable and does not run in any fallback directory.
This preserves cleanup actions' directory boundary. Down, repeated up, and
destroy use the same rule. A liveness probe whose launch needs that vanished
directory invalidates the node and attempts cleanup in the stored setup
directory; it never treats the missing directory as release evidence.

`[<id>.outputs]` is the workflow's explicit public projection record;
`outputs_schema` declares it. Each binding is evaluable from node outputs as
soon as its sources have been produced. The record is derived from the
session's revision and node outputs, never stored: a binding follows its
sources when a node is recreated and is unset while any source is not produced.
Once every binding has a value, the record must satisfy `outputs_schema`; a
record that does not is not published. A cleanup also
reads the last recorded outputs of a node that is no longer produced, so a
dependent of a failed node can still be released; a setup never does.
`workflow.outputs.*` is the only workflow-output root for display,
instructions, node inputs, and channel delivery.

```toml
[pull_review.outputs]
owner_endpoint = { from = "nodes.orchestrator.outputs.endpoint" }
instruction    = { from = "nodes.agent.outputs.instruction" }

[pull_review.outputs_schema]
type     = "object"
required = ["owner_endpoint", "instruction"]

[pull_review.outputs_schema.properties]
owner_endpoint = { type = "string" }
instruction    = { type = "string" }

[pull_review.display]
title  = { from = "workflow.outputs.instruction" }
status = "review"
```

## Event channels

`[[<id>.event.channel]]` selects a channel definition instead of an effect and
adds an `include` allowlist of event-type globs. Its `inputs` are values over
the same roots node inputs use, evaluated at delivery. `name` identifies the
binding within the workflow; two bindings may select the same channel under
different names and includes.

An `include` entry matching `plect.instruction` also selects
`plect.session.resumed`, even when the entry does not match that event's type.
This applies to every channel binding with that include, regardless of its
delivery target.

## Node lifecycle

`plect.node.result` is appended to a session's log whenever a node's setup,
cleanup, or liveness verification completes, or a produced node is skipped
after liveness passes. It applies equally to manual, child, and
population-produced sessions, independently of population up/down events.

| Metadata key | Meaning |
|---|---|
| `node` | The node id. |
| `effect` | The node's `uses` target. |
| `scope` | `session` or `run`. |
| `action` | `setup`, `cleanup`, or `alive`. |
| `result` | `produced`, `skipped`, `failed`, or `cleaned`. |
| `duration_ms` | How long the action took. |

`body` carries a bounded stderr or error tail only for `failed`; persisted
outputs remain the authority for produced and cleaned nodes.

## Workflow revisions

Session creation stores the expanded workflow as a revision: nodes, binding
expressions, health and healthcheck declarations, event channels, ticks, and
instruction text, together with every effect and task declaration the session
can execute, including dynamic task instances, resolved nesting, and cascade.
The revision's SHA-256 digest is its identity, so equal expansions share one
revision and an edit to any captured declaration changes the digest.

`up`, `down`, `destroy`, repair, health, tick, delivery, and cleanup execute the
session's revision. A configuration edit does not reach an existing session,
and the original workflow file need not exist. Only the trusted configuration
tree expanded by serve or the CLI writes a revision; runtime inputs, untrusted
messages, and stored execution results cannot.

### Rebase

`plect workflow rebase <session>... | --all` expands the workflow from current
trusted configuration, classifies the difference from the session's revision,
and moves the session to the new revision when the class permits. `--all`
covers every active session with a workflow and `--workflow <name>` narrows it.

| Class | Raised by | Outcome |
|---|---|---|
| compatible | No difference below. A difference only in instructions, display, event channels, ticks, workflow-level `[<id>.health]`, or the `description` or `instruction` of a task document is compatible. | The revision is replaced. |
| automatic | A node added or removed; an alive probe changed on a node or a live dynamic instance, with the rest of its declaration unchanged. | Removed nodes, and nodes that depended on them, are cleaned with the old revision before the switch, whatever the session's status. Added nodes are set up by an `up` on the new revision, which a rebase runs only when the session is up. |
| restart | A run-scoped node with unchanged scope whose node entry, effect definition, execution directory, or a public output its inputs read changed; every run-scoped node replaced by different ones; a live run-scoped dynamic effect instance whose definition changed. | Refused unless `--with-restart`, which brings the session down on the old revision, cleans removed nodes, switches, and brings an up session back up. |
| recreate | A node whose scope changed, or a session-scoped node whose declaration, execution directory, or a public output its inputs read changed; a changed entry resource; for a live dynamic instance, a removed or missing definition, a changed session-scoped effect definition, or a changed task document beyond its `description` or `instruction`; saved outputs that a new node input, alive binding, nesting layer, or event channel input cannot resolve from; a release of the workdir's producer while a live dynamic instance runs in that directory. | Always refused; destroy and recreate the session. |

A session takes the highest class any difference raises, and the command prints
each reason. `--dry-run` prints the classification and the nodes to recreate
without changing the session. A refusal by class leaves the revision unchanged.
Every classified, non-dry attempt appends one `plect.session.rebase` event
recording the digests, class, and result, except an attempt that the session
guard or the lifecycle relation guard refuses; a refusal by class is recorded.

## Execution records

Each setup attempt has a session-owned execution record, including partial and
failed attempts. It records the acquired resource identity, setup facts,
directory, nested-layer facts and environment, and the dependency edges and
allocation-lifetime information needed to release the existing plan. It does
not retain executable cleanup code, plugin binaries, a replayable cleanup
declaration, per-layer refusal digests, or plugin content pins. The local
session-state store protects writes to this evidence but is not a trust boundary
for executable code.

Retained execution records are a retained execution plan. Release follows its
recorded dependency order rather than an order derived from the session's
revision: an old agent depending on an old checkout is cleaned up before that
checkout is released. This preserves the lifetime boundaries of allocations
whose declarations were removed or changed.

The current operation supplies only `force`; it does not replace setup-time
facts. A record that still matches a node of the
session's revision remains in use. A node's effect, scope, or declaration
changes only through a rebase, which classifies the change as described in
[Rebase](#rebase). A node removed by a rebase is not set up again, but its
record remains available for cleanup and release.
Population reload is separate: its current policy controls future evaluation,
while members and their provenance remain retained as specified below.

## Cleanup and reconstruction

Before teardown, plect resolves the matching cleanup layer from the session's
revision. A missing definition, valid project trust, setup fact, directory,
credential, environment, or reliable target identity is a cleanup precondition
failure. The record stays
inspectable with its outstanding obligation and a non-secret reason; cleanup
never falls back to another directory or infers release.

Release follows execution-owned dependency edges, dependents before
prerequisites. A failed or unavailable dependent blocks release of its
prerequisites but not independent allocations. Successful cleanup marks only
that execution released. An externally released allocation requires the
explicit, audited acknowledgement operation proposed in [the cleanup
ADR](../adr/2026-09-08-minimum-cleanup-contract.md); its assertion is not a
successful cleanup and cannot release another generation.

Ordinary `up` can retry cleanup but cannot reconstruct an allocation or a
required prerequisite while its release obligation remains outstanding.
`--force-recreate` follows the same release-then-new-generation path and never
acknowledges or discards an obligation; the cleanup it runs receives
`force = true`, because the node is rebuilt right after.

`destroy --force` ignores destroy's safeguards and errors and runs it to the
end:

- It destroys a session that has child sessions, orphaning them.
- It removes a session with no stored workflow revision, without cleanup.
- A failed cleanup becomes a warning. Destroy records that release was not
  verified and removes the session's remaining execution records.
- The prerequisites of a node whose cleanup failed are released anyway.
- Every effect's cleanup receives `force = true`, so it may override its own
  safeguards as well; see [effects](effects.md#cleanup-and-force).

Whatever those safeguards protected — uncommitted changes, child sessions, an
allocation whose release nobody verified — can be lost. `destroy --force` is not
evidence that an external resource was released.

## Display

`[<id>.display]` declares values the CLI and Plecture Web UI render. They read
public outputs derived from the session's revision and node outputs, never the
network, so their freshness follows the node output update cadence.

## Clocks

`[<id>.tick]` declares when the tick reactor advances a session, in addition to
the judge builtin trigger. `on` lists event-type globs; `heartbeat` ticks after
that quiet duration; and `max_heartbeat` caps quiet-tick backoff. Omitting all
of them leaves manual ticks and the judge builtin as the only drivers.

`backoff_reset` names which conditions reset the quiet-tick backoff counter to
0 at a heartbeat sweep, holding the interval at `heartbeat`:

- `"inbound"` — an inbound event arrived since the last heartbeat sweep.
- `"fingerprint"` — the session's own composite done_when fingerprint changed.
- `"live_children"` — at least one session directly parented on this one is
  up. Read from the sessions table only; no probe, no child fingerprint.

Each declared condition is evaluated at both points the reactor consults the
backoff counter: before gating a heartbeat sweep (so a condition holding
*right now* makes the next tick due at `heartbeat`, not whatever interval the
counter had already grown to) and after a tick actually runs (so the counter
itself persists as reset). `"inbound"` and `"fingerprint"` hold when they
occurred since the last heartbeat sweep; `"live_children"` holds when it is
true at that instant.

The default, when `backoff_reset` is absent, is `["inbound", "fingerprint"]`.
Declaring the field replaces that default wholesale rather than adding to it,
so `backoff_reset = ["fingerprint"]` means inbound no longer resets. The list
must not be empty, and every name must be one of the three above — either
failure is a load-time error, since a tick that can never reset is a
misdeclaration. `[tick]` is workflow-level only: `config.toml` has no
defaults mechanism a workflow-level `[tick]` field falls back to, so there is
no global default for `backoff_reset`.

A heartbeat sweep's `kick` action body lists the session's up direct children
(name, run state, health, minutes since their own last tick) when any exist,
independent of `backoff_reset` — enough for the dispatcher receiving the kick
to decide which children it can safely bring down.

`[<id>.healthcheck]` declares `period`, `stall_threshold`, and
`renotify_every`. It controls sampling cadence, not what health means; effect
`[health]` declarations define that meaning. `tick` and `healthcheck` are
whole-table runtime tuning: a later workflow replacement replaces each table,
not individual keys.

`[<id>.health]` optionally permits automatic repair with
`auto_repair_attempts`, a required non-negative integer when the table is
declared. The default is `0`, which disables repair. `resume_instruction` is an
optional string. Its default instructs the agent to check the state of its
work and of the resources it was acting on before continuing after an
interrupted runtime, and names no particular tool or service; wording specific
to a VCS, forge, or chat service belongs in the workflow's own
`resume_instruction`, as in the example below. An empty string disables the
instruction. A later workflow table replaces an
inherited `[health]` table as a whole; setting `auto_repair_attempts` to `0`
disables inherited permission, while omitting `resume_instruction` selects the
default text. For example:

```toml
# plect-fixture: result=valid entry=definitions

[review_workflow]
kind = "workflow"
resource = "issue_pr"

[review_workflow.health]
auto_repair_attempts = 2
resume_instruction = "Check the worktree and the review thread before continuing."
```

When `Up` re-sets up at least one run-scoped node whose setup receives `prev`,
core publishes one inbound `plect.session.resumed` event even if only a sidecar
is rebuilt while the agent runtime is reused. Its body is the effective
instruction, and `trigger` metadata is `repair`, `up`, or `wake`. A first
launch and an `up` that reuses every run-scoped node publish no event. The
event is delivered through any channel whose `include` matches
`plect.instruction`.
The [event reference](events.md#session-resume-instructions) defines each trigger.

The health cycle may run plain `up` only while
the session's recorded status is `up`, after a non-zero `alive` exit (a
`process` probe that finds its pid gone or reused counts as one), a failed
or missing current-plan node, or a down run state. Probe errors that prevent
evaluation do not permit repair. Each started repair consumes one attempt until
a healthy observation or successful human `up` resets the budget. Exhaustion
reports `repair_suspended`; three consecutive cycles with an unevaluable
`alive` probe report `repair_undecidable` without consuming an attempt.

While repair is still expected, a channel delivery that fails is held rather
than recorded: no `plect.channel.error` is written and the delivery cursor stays
at the failed event, so later events wait behind it and the event is retried
until delivery succeeds. Repair is expected for a session that opted in, whose
fresh health observation permits repair, and that reports neither
`repair_suspended` nor `repair_undecidable`. A hold ends when repair stops being
expected, or once the event is thirty minutes old; the failure is then recorded
as `plect.channel.error` and delivery moves on. A failure before delivery, such
as a channel input that does not resolve, is never held, and a session that has
not opted in is never held.

An agent runtime whose worker can stay up while every turn fails composes
core's process check with a script that reads the turn outcome. The script runs
only while the pid is still the recorded process:

```toml
[headless_runtime.health.alive]
process = "self.outputs.pid"
type    = "shell"
script  = '''
HEALTH="$state_dir/health.json"
[ -f "$HEALTH" ] || exit 0
jq -e ".last_exit_code == 0" "$HEALTH" >/dev/null
'''

[headless_runtime.health.alive.bind]
state_dir = { from = "self.outputs.state_dir" }
```

## Concurrency

`max_up_children` optionally caps sessions parented on a session this workflow
produces that may hold run state `up` at once. `plect up` rejects a child that
would exceed the cap, naming the parent, cap, and current count; it does not
queue the request, so its caller retries after capacity frees.

A child counts while it holds run state `up` and stops counting when it goes
down or is destroyed. An admitted `plect up` in flight also counts until its
process is confirmed gone. An idempotent re-up of an already-up child is
exempt, because it is already counted. `--force-recreate` is not exempt: it
holds a new admission while rebuilding. A second up for the same child while
the first is running is rejected outright; once the first process is confirmed
gone, a retry reclaims the admission, and destroy clears it immediately.

An admission remains while its up process legitimately runs, not for a fixed
timeout. An omitted cap is unlimited.

## Populations and chains

A population belongs to a workflow and uses its entry resource type. A
`resource` field in an undelivered-publish population must name that same
type; it cannot declare an independently authoritative resource.
A population is deployment policy, declared only in user-owned global or
trusted selected-project configuration. Its identity is its stable
configuration-selection context, the containing workflow's resolved address,
and its unique `name`. The context is the selected canonical project root or
the distinct global-only context when no project root is selected. That
provenance is stored on every admitted session and is required for later
mutation or destruction.

| Field | Meaning |
|---|---|
| `name` | Required stable identifier, unique within the workflow. |
| `query` | Literal parameters validated by the entry resource's query input schema; required with `uses`. |
| `uses` | Non-empty query means, such as `poll` or `subscribe`; required with `query`. |
| `resource` | Entry resource type for an undelivered-publish source; required with `where`. |
| `where` | Expression over `match.*` captures; required with `resource`. |
| `session.task` | Optional caller-selected initial task, compatible with the entry resource. |
| `session.inputs` | Optional values over literals and `resource.id`, plus `item.*`/`query.*` for query sources or `match.*` for undelivered publishes. |
| `session.destroy.force` | Whether automatic destruction uses force; default false. |
| `poll_every` | Required positive duration when `uses` selects `poll`; forbidden otherwise. |
| `expire_after` | Required positive quiescence duration without `poll`; forbidden with it. |
| `auto_down` | Permits capacity-pressure down selection; default false. |
| `auto_destroy` | Permits guarded destruction; default false, which records a dry run. |

Exactly one source form is required: the workflow's resource observer with
`query` and `uses`, or `resource` with `where`. Combining the forms fails
loading. The
second source accepts a resource-addressed publish only when it has zero
subscribers and the resource's captures satisfy `where`. Core creates the
member session, subscribes it, and appends that event in one step. It does not
refetch event history. `session.inputs` may project a literal query parameter
as `query.<key>` only for the query source form.

```toml
[thread_worker]
kind = "workflow"
resource = "slack_thread"

[[thread_worker.populations]]
name = "new_mentions"
resource = "slack_thread"
where = "match.channel_id == 'C0123'"
expire_after = "8h"
```

For a query population, `uses` is the sole authority for query means. A
population naming only `poll`
does not start subscribe even if the resource declares it; one naming only
`subscribe` does not poll. No default permits a later plugin-added means to
start in an existing deployment.

With `poll`, a complete validated snapshot is the sole membership and absence
authority. Subscribe appearances can admit or re-up a member but cannot undo a
poll absence tombstone; only a later positive poll opens a new generation.
Without `poll`, expiry measures successful session creation and resets only on
accepted repeated appearances or inbound session events; silence, failure, and
restart do not prove absence. Deselecting poll deliberately loses absence
detection and missed-event repair, so enumerable resources normally retain it.

Changing `uses` on config reload retains owned sessions and provenance, because
policy replacement is not resource evidence. The evaluator re-derives
membership using the new means alone. An invalid resident reload retains the
last valid evaluator.

Destruction waits until every dynamic task with `done_when` is satisfied. A
missing predicate, observation failure, evaluation failure, or pending leaf
blocks it. At virtual-root capacity, only an up, population-owned session from
an `auto_down` population is eligible. Its latest durable status must be an
explicit clear newer than creation, accepted appearance, and inbound events.
Eligible members are selected by oldest activity then session name and run
ordinary cleanup. An appearance, inbound event, or positive poll requests up
again. Removing or invalidly changing provenance never lets another population
adopt existing sessions.

```toml
[standing_cases]
kind     = "workflow"
resource = "query_source"

[[standing_cases.populations]]
name         = "dispatch"
uses         = ["poll", "subscribe"]
poll_every   = "5m"
auto_down    = true
auto_destroy = false

[standing_cases.populations.query]
scope = "open"

[standing_cases.populations.session]
task = "population_task"

[standing_cases.populations.session.inputs]
context = { from = "item.context", optional = true }

[standing_cases.populations.session.destroy]
force = false
```

Population decisions are durable events:

| Event | Meaning |
|---|---|
| `plect.workflow_population.up` | A member transitioned to up. Re-admitting an up member records nothing. |
| `plect.workflow_population.admit_ok` | An admit attempt succeeded, including one that found the member already up. |
| `plect.workflow_population.down` | Capacity policy selected or evaluated a down action. |
| `plect.workflow_population.destroy` | An eligible member was destroyed. |
| `plect.workflow_population.destroy_deferred` | A task guard blocked destruction. |
| `plect.workflow_population.destroy_dry_run` | Destruction was eligible but disabled. |
| `plect.workflow_population.conflict` | Existing state has incompatible provenance. |
| `plect.workflow_population.failure` | A query, admit, eviction, or destroy operation failed; `reason` metadata says which (`poll`, `poll_validation`, `subscribe`, `subscribe_item`, `down`, `destroy`, or an admit attempt's own `capacity`, `up`, `input`, `task_setup`). |

A member's admit history — carried only in the events above, not a
separate persisted field — decides whether it keeps head-of-line priority
at the capacity gate: only a member whose most recent outcome was
`admit_ok`, or a `failure` whose `reason` is one of the four admit-attempt
values above and equals `capacity`, does. A `failure` with any other
reason, or a `conflict`, disqualifies it, the same as any other admit
failure. `plect workflow populations <workflow-id> <population-name>`
shows each member's current status, including its last admit error and how
many attempts have failed in a row since the last `admit_ok`.

A wake-suspended member remains pending. Population admission waits with a
retry interval that grows from five seconds to one minute without consuming its
admission failure budget. A new inbound event requests another wake and resets
the wait, after which admission sets up the initial task.

A task chain may start another session under a selected workflow once its
condition holds. It addresses the same resource by default or another concrete
resource it observed, such as a pull request created from issue work. The
target workflow must accept that resource. Judge acceptance remains associated
with the completion condition and revision/evidence contract that declared it.

## Representative configurations

The shared-environment pattern has specialized workflows for issue work and
pull review. The duplication is intentional: their entry resource types are
different even when their checkout, terminal, agent, and delivery nodes look
similar. Migrating one workflow from issues to pull requests creates a second
workflow, moves callers and populations to it, and retires the old one only
after its sessions drain.

```toml
[issue_work]
kind     = "workflow"
resource = "issue"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[issue_work.nodes]]
id = "checkout"
uses = "checkout_effect"
[[issue_work.nodes]]
id = "agent"
uses = "agent_runtime"

[pull_review]
kind     = "workflow"
resource = "pull_request"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[pull_review.nodes]]
id = "checkout"
uses = "checkout_effect"
[[pull_review.nodes]]
id = "reviewer"
uses = "agent_runtime"

[conversation]
kind     = "workflow"
resource = "conversation"
workdir  = { from = "nodes.thread_dir.outputs.workspace_dir" }

[[conversation.nodes]]
id = "thread_dir"
uses = "thread_directory"
[[conversation.nodes]]
id = "agent"
uses = "agent_runtime"

[[conversation.populations]]
name       = "open_conversations"
uses       = ["poll", "subscribe"]
poll_every = "5m"
auto_down  = true
auto_destroy = true

[conversation.populations.query]
status = "open"

[conversation.populations.session]
task = "conversation_triage"
```

The conversation population retains its directory and agent environment when
it adds an `issue_investigation` task bound to an issue resource. Its
capacity-driven down/up and guarded destruction remain population policy;
neither turns the issue into the session's entry resource.

## Validation rules

- `resource` resolves to one `resource` definition.
- A selected entry identifier resolves to that resource type.
- An omitted workflow is valid only with exactly one accepting workflow.
- `workdir` is a projection from a node output declared by this workflow.
- A `workdir` resolves to an absolute local filesystem directory path.
- Node dependencies plus derived default-workdir edges have no cycle.
- A population uses its containing workflow's resource. It has exactly one
  source form: `query`/`uses` or `resource`/`where`; the latter names the entry
  resource type and reads only declared `match.*` captures.
- An initial task is caller-selected and compatible with its concrete binding.
- Workflow public outputs satisfy `outputs_schema` and bind only declared node
  outputs or allowed session inputs.
