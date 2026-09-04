# Standing session dispatch from resource discovery

## Context

Plecture can dispatch one session explicitly and can let a task instance spawn
another session through a chain. It cannot declare a standing population:
for every external resource matching deployment-owned conditions, keep one
session present, and remove that session when the resource no longer matches.

A downstream deployment consequently carries a large reconciliation program
for pull-request review dispatch. A second deployment scenario needs the same
policy for operations conversations, but its source is a pushed chat mention
rather than an enumerable query. Other deployments need the enumerable form
for orchestrator polling. These are three concrete consumers of one missing
language concept, not a speculative extension point.

The existing language already separates the responsibilities this feature
must join:

- A resource observer recognizes one resource, observes its live state, and
  declares the `resource.state.*` contract
  ([resource observers](../language/resource-observers.md)).
- A task document declares which observer its work is written for, and its
  completion and chains read the observer's facts
  ([tasks](../language/tasks.md)).
- A workflow declares the effects and workspace provider that house a session
  ([workflows](../language/workflows.md)).
- A chain is colocated with the task whose instance facts it reads, allowing
  its condition, target workflow, and inputs to be validated before it fires
  ([chains](../language/chains.md)).
- Definition blocks, rather than files, carry identity. They share one
  namespace per layer, and topology references are static
  ([declarations](../language/declarations.md)).
- Each value surface exposes only the roots that exist at that point, and live
  `resource.state.*` reads are coherent within one evaluation
  ([values](../language/values.md)).
- Conditions use the existing check and CEL expression leaves. Plecture does
  not add a predicate language of its own
  ([tasks](../language/tasks.md),
  [language overview](../language/README.md)).

The missing construct is deployment policy. A plugin can say how resources
are discovered, but it cannot choose a team's repositories, labels, chat
channels, workflow, task, concurrency, or retention policy. Conversely, a
workflow describes one session but is not intrinsically the owner of every
resource population that may dispatch it.

This decision concerns desired session presence. It does not change whether a
persisted node is still valid, whether `plect up` verifies before skipping it,
or how a partially failed run is represented. Those recovery questions remain
the responsibility of [the failure-model work](https://github.com/kecbigmt/plecture/issues/371).

## Decision

### 1. Placement: use a deployment-owned definition

#### Options

| Option | Load-time validation | Layer and cascade owner | Plugin-boundary consequence | Assessment |
|---|---|---|---|---|
| (a) New top-level kind | The task, its observer and `discover` face, the workflow, both input contracts, predicate roots, and item roots all resolve from one definition. | A trusted user or machine layer owns it. It is a whole-definition kind, so a deeper same-id declaration replaces it. Workspace overlays cannot declare it. | Plugins own discovery mechanics and schemas; deployments own parameter values and session policy. | Clean responsibility and complete static validation, at the cost of adding one kind. |
| (b) Nested in `workflow` | The workflow and session inputs are local, but lifecycle facts point through a task to an observer outside the workflow. | Workflow cascade would make a population policy append to or replace runtime wiring. A plugin workflow could not contain team values, while an overlay would need to amend plugin-owned policy. | It invites deployment data into shipped workflows and makes manual, chain, and discovered uses of one workflow unexpectedly share population semantics. | Rejected: the reference direction is twisted and a workflow is reusable housing, not a population owner. |
| (c) Nested in `task` beside chains | The observer facts are local and a target workflow can be resolved like a chain. | Task `extends` is additive, so source policy would compose through task specialization even though only one deployment should own a population. | Shipped tasks would either contain team data or require a user to replace or extend plugin work merely to choose deployment policy. | Rejected: chains fire from an existing instance; discovery fires specifically because no instance exists yet. |
| (d) Added to `config.toml` | References and schemas could be checked, but the entry would have no definition identity of its own. | The reserved file is machine-wide resolution and defaults, outside definition discovery and cascade. | It would centralize every deployment rule in a reserved file and provide no address for provenance or events. | Rejected: it violates the definition-block principle and cannot name the authority allowed to destroy a session. |
| (e) Nested in `resource_observer` | Discovery, item schema, and lifecycle fact roots would be colocated. The workflow and task could also be resolved. | Resource observers are whole-definition plugin resources; a user cannot append policy to one and replacing one copies plugin mechanics into the deployment layer. | Team repositories, channels, limits, and workflow choices would land in plugin-owned files or force a fork. | Rejected: the observer should gain the reusable discovery mechanism, not deployment policy that consumes it. |

#### Recommendation

Add a top-level `session_source` definition. The cost of a kind is justified
because it is the independently addressable owner of a durable relationship:
one discover face supplies a desired population of sessions built by one
workflow. Neither endpoint owns that relationship.

A `session_source` is accepted only from trusted user and machine definition
roots. Plugin definition roots may declare resource observers with discover
faces and may ship source examples outside `config/`, but cannot activate a
source. The untrusted workspace overlay does not load this kind.

`session_source` is a whole-definition kind. Its fully resolved definition
address is persisted as creator provenance on every session it creates. A
clean replacement of the same definition id continues to own those sessions;
an invalid config reload keeps the last valid evaluator, and clean removal of
the definition stops its evaluator without destroying its sessions. Removing
configuration is not evidence that any resource left a successfully observed
set; the operator can destroy the provenance-marked sessions explicitly or
first replace the source with parameters whose successful snapshot is empty.

### 2. Kind name: `session_source`

#### Options

| Candidate | Kind-vocabulary reading | Tradeoff |
|---|---|---|
| `session_source` | A role compound: the declaration sources sessions that exist apart from it. | Names the produced object and follows `workspace_provider` / `resource_observer`; “source” alone does not convey reconciliation, which the contract must explain. |
| `resource_intake` | A role compound centered on incoming resources. | Reads as one-way ingestion and underweights destruction and continued convergence. |
| `dispatch_rule` | A rule that dispatches. | Names a serialization mechanism rather than a runtime responsibility, and “dispatch” suggests a one-shot action. |
| `session_controller` | A role compound centered on reconciliation. | Overstates authority: it does not control a session after creation beyond source-owned lifecycle. |
| `standing_dispatch` | Describes the use case. | “Standing” is lifecycle prose and “dispatch” is an action, not the thing the declaration produces or observes. |

#### Recommendation

Use `session_source`. The declaration produces and retracts desired sessions
from a resource source, so the role-compound rule in
[declarations](../language/declarations.md) applies. The bare concept is not
used because a source resource exists apart from its declaration, and no kind
name includes `definition` or `rule` merely to describe its syntax.

### 3. Discover contract: a third resource-observer face

#### Options

1. Model polling and pushing as unrelated plugin APIs. This matches their
   transport but duplicates item validation, parameter binding, provenance,
   and dispatch semantics.
2. Let a discover command print resource ids only. This cannot carry the
   triggering mention timestamp required to initialize a pushed chat session,
   and adding ad-hoc environment variables would make those values
   uncheckable.
3. Give both modes one parameter schema and one item schema, while keeping
   their execution shapes distinct.

#### Recommendation

Add optional `[<observer>.discover]` to `resource_observer`. Its closed shape
is:

| Field | Requirement | Meaning |
|---|---|---|
| `mode` | Required; `poll` or `push`. | Selects complete-snapshot or appearance-stream semantics. |
| `type`, `bin`/`command`, `args` or `script`/`bind` | Required action. | Runs the plugin-owned discover implementation using the existing action variants. |
| `inputs_schema` | Required JSON Schema object. | Declares deployment parameters consumed by the action as `inputs.*`. An empty object schema is valid. |
| `item_schema` | Required JSON Schema object. | Declares every discovery record. It must require a string `resource` property. |

The action's `inputs.*` root is the source definition's literal
`discover_inputs` object, validated against `inputs_schema` before the action
runs. No session, workspace, node, or resource exists yet, so no other data
root is exposed. Capabilities remain available only in the action positions
that already accept them.

A poll action runs to completion and writes one JSON array. That array is the
complete current membership set for those parameters; successful exit means
pagination is complete. A push action is a supervised stream and writes one
JSON object per line as resources appear. Stream termination or a non-zero
exit is a discovery failure and is restarted with the resident supervisor's
bounded backoff. It never implies that any resource disappeared.

Both outputs validate each object against `item_schema`. Within a
`session_source` session input, the item's declared properties are available
as `discovery.*`, while its required `resource` property also becomes the
`resource.id` passed to session dispatch. These are per-item production
records, not live roots. They are evaluated when a session is first created;
a later discovery of the same resource does not mutate frozen session inputs.

A source's required static `session.task` reference is the single authority
for the observer. The loader resolves the task, resolves that task's
`resource_observer`, and requires the observer to declare `discover`. There is
no second resource-observer reference on `session_source` that could disagree
with the task.

For each evaluation, core first validates the action result, all items,
resource matches, workflow dispatch, lifecycle observations, and input
bindings. A malformed item, duplicate resource, incomplete or failed action,
unresolved root, observer mismatch, or workspace-provider mismatch fails the
whole evaluation before any create or destroy. A failing discover therefore
dispatches and destroys nothing. One bad push item fails only that item's
evaluation; it does not retract earlier appearances.

#### Poll discover sketch

The GitHub plugin extends its existing pull-request observer with a complete,
enumerable search. The added `lifecycle_state` fact gives lifecycle policy a
declared key rather than asking core to interpret provider-specific states.

```toml
[pull]
kind  = "resource_observer"
match = '^https://github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/pull/(?P<number>\d+)'

[pull.observe]
type = "exec"
bin  = "github-issue-pr"
args = ["observe", "--resource", { from = "resource.id" }]

[pull.discover]
mode = "poll"
type = "exec"
bin  = "github-issue-pr"
args = [
  "discover-pulls",
  "--repositories", { json = { from = "inputs.repositories" } },
  "--labels", { json = { from = "inputs.labels" } },
  "--state", { from = "inputs.state" },
  "--draft", { expr = "inputs.draft ? 'true' : 'false'" },
]

[pull.discover.inputs_schema]
type                 = "object"
required             = ["repositories", "labels", "state", "draft"]
additionalProperties = false

[pull.discover.inputs_schema.properties]
repositories = { type = "array", minItems = 1, items = { type = "string" } }
labels       = { type = "array", items = { type = "string" } }
state        = { type = "string", enum = ["open", "closed", "all"] }
draft        = { type = "boolean" }

[pull.discover.item_schema]
type                 = "object"
required             = ["resource"]
additionalProperties = false

[pull.discover.item_schema.properties]
resource = { type = "string", format = "uri" }

[pull.state_schema]
type     = "object"
required = ["resource_kind", "checks_status", "revision", "pr_url", "mergeable_state", "review_decision", "lifecycle_state"]

[pull.state_schema.properties]
resource_kind   = { type = "string", enum = ["pull"] }
checks_status   = { type = "string", enum = ["SUCCESS", "PENDING", "FAILURE", "NULL"] }
revision        = { type = "string" }
pr_url          = { type = "string" }
mergeable_state = { type = "string", enum = ["clean", "dirty", "unstable", "blocked", "behind", "unknown", "draft", "has_hooks"] }
review_decision = { type = "string", enum = ["APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", "NULL"] }
lifecycle_state = { type = "string", enum = ["open", "closed", "merged"] }
```

#### Push discover sketch

The Slack plugin adds a thread observer. Its discover action connects to the
resident adapter's unbound-mention stream; it does not open a second Socket
Mode connection. The adapter remains the integration-specific receiver, while
the action converts its stream to the generic discovery-record contract.

```toml
[thread_state]
kind  = "resource_observer"
match = '^https://[A-Za-z0-9.-]+\.slack\.com/archives/[A-Z0-9]+/p\d{16}(?:\?thread_ts=\d{10}\.\d{6}&cid=[A-Z0-9]+)?$'

[thread_state.observe]
type = "exec"
bin  = "slack-adapter"
args = ["resource", "observe", "--resource", { from = "resource.id" }]

[thread_state.discover]
mode = "push"
type = "exec"
bin  = "slack-adapter"
args = [
  "discover", "unbound-mentions",
  "--base-url", { from = "inputs.base_url" },
  "--channel-ids", { json = { from = "inputs.channel_ids" } },
]

[thread_state.discover.inputs_schema]
type                 = "object"
required             = ["base_url", "channel_ids"]
additionalProperties = false

[thread_state.discover.inputs_schema.properties]
base_url    = { type = "string", format = "uri" }
channel_ids = { type = "array", minItems = 1, items = { type = "string" } }

[thread_state.discover.item_schema]
type                 = "object"
required             = ["resource", "channel_id", "thread_ts", "mention_ts"]
additionalProperties = false

[thread_state.discover.item_schema.properties]
resource   = { type = "string", format = "uri" }
channel_id = { type = "string" }
thread_ts  = { type = "string" }
mention_ts = { type = "string" }

[thread_state.state_schema]
type                 = "object"
required             = ["linked_issue_status"]
additionalProperties = false

[thread_state.state_schema.properties]
linked_issue_status = { type = "string", enum = ["open", "none"] }
```

The adapter's
[`on_unbound_mention`](https://github.com/kecbigmt/plecture/issues/362) hook is
subsumed, not retained as a second dispatch authority. During rollout it can
feed the discover stream while the generic contract is proved, but the
supported end state has the adapter expose the appearance stream directly and
removes the opaque command hook with the required one-time migration. Keeping
the hook indefinitely would leave two authorities deciding whether one
mention creates one session.

### 4. Session and lifecycle surface

#### Options

1. Let the plugin discover action return complete `plect up` commands. This
   puts workflow and team policy in executable output and makes topology
   dynamic.
2. Give each source its own predicate syntax and timeout expressions. This
   creates a second language for facts already expressible by task conditions
   and moves duration arithmetic into CEL.
3. Make session topology static, session values schema-checked, and lifecycle
   conditions reuse existing fact leaves.

#### Recommendation

Use this closed `session_source` surface:

| Field | Requirement | Meaning |
|---|---|---|
| `kind` | Required; `session_source`. | Selects this definition contract. |
| `discover_inputs` | Required table. | Literal deployment data validated against the task observer's discover `inputs_schema`. |
| `session.workflow` | Required static workflow reference. | Selects the session's effect and workspace shape. |
| `session.task` | Required static task reference. | Selects the initial task and, through it, the sole observer/discover authority. |
| `session.inputs` | Optional value table. | Session inputs over literals, `resource.id`, and the discover `item_schema`'s `discovery.*` properties. |
| `session.max_sessions` | Required positive integer. | Bounds sessions owned by this source. |
| `poll_every` | Required positive duration for poll; forbidden for push. | Sets complete-snapshot cadence. |
| `idle` | Required positive duration for push; forbidden for poll. | Makes a push-owned session eligible for expiry after no inbound activity. |
| `grace` | Optional non-negative duration for push; forbidden for poll; default zero. | Requires idle eligibility to remain continuous before destruction. |
| `destroy_when` | Optional conjunction of check or expression leaves. | Destroys an owned session when current resource facts satisfy it. |
| `keep_while` | Optional conjunction of check or expression leaves; push only. | Suppresses idle expiry while current resource facts satisfy it. |

`session.task` is folded into the effective session input object as `task`,
using the same shorthand as `plect up --task`. `session.inputs` may not also
declare `task`. The complete effective input object is validated against the
target workflow's `inputs_schema` at load time: literal types are checked
directly, `resource.id` is a string, and every `discovery.*` path is resolved
against the observer's discover `item_schema`. Runtime validation still
checks schema constraints that static projection cannot prove.

`session.workflow`, `session.task`, and the observer reached through the task
are static topology. They accept the ordinary relative or catalog-qualified
reference grammar and cannot be CEL expressions. A workflow's workspace
provider must match each discovered `resource`; that provider remains the
single authority for session naming.

`destroy_when` and `keep_while` use the check and expression leaf forms from
`done_when` and chain `when`, but expose only `resource.state.*`. Judge leaves
and `self.state.*` do not exist at this declaration: session-source lifecycle
is about the external resource, not one task instance's review record. Keys
resolve at load time against the task's observer `state_schema`. Every
lifecycle decision observes once, and all leaves read that coherent snapshot.
An observation or expression failure is fail-closed and destroys nothing.

A successful poll snapshot has two removal paths. An owned resource absent
from the complete snapshot is no longer a member and is destroyed. A present
resource satisfying `destroy_when` is also destroyed and is not recreated in
the same pass. Thus a removed label and a merged pull request both converge
without treating a failed or partial query as absence.

A push stream cannot prove non-membership. For push, `idle` measures from the
latest of session creation, a repeated appearance, or an event already
recorded on that session with `direction = "inbound"`. Internal ticks,
lifecycle events, and outbound agent events do not keep a conversation alive.
After `idle` elapses, `keep_while` is evaluated. A true or failed evaluation
resets expiry eligibility. A false result starts `grace`; eligibility must
remain continuous through that duration before destruction. `destroy_when`
remains an independent immediate resource-fact path.

`session.max_sessions` is an admission guard, not a priority or lifecycle
rule. Existing owned members are considered before new resources, new
candidates are ordered by resource id, and the source creates only enough to
reach the cap. Reducing the cap does not evict sessions; ordinary absence,
`destroy_when`, or push expiry must make them eligible for destruction. This
avoids inventing a ranking language.

Creation and destruction use the same service paths as `plect up` and
`plect destroy`, including resource allowlists, workspace-provider resolution,
cleanup, and errors. A source persists its definition address on sessions it
successfully creates and may destroy only those sessions. It never adopts an
existing session with the same derived name. A source/chain or source/source
name collision emits a conflict and leaves the existing session untouched.

#### Review-dispatch configuration

This complete user-owned TOML binds the GitHub poll face above to an existing
review task and a review workflow. It replaces an external reconcile loop.

```toml
[review_agent]
kind               = "workflow"
workspace_provider = "official.github.worktree"

[review_agent.inputs_schema]
type                 = "object"
required             = ["task"]
additionalProperties = false

[review_agent.inputs_schema.properties]
task        = { type = "string", enum = ["official.github.review"] }
instruction = { type = "string" }

[[review_agent.nodes]]
id   = "pane"
uses = "official.tmux.pane"

[[review_agent.nodes]]
id   = "agent"
uses = "official.claude.runtime"

[[review_agent.event.channel]]
name    = "runtime"
uses    = "official.claude.delivery"
include = ["plect.instruction", "user.emit", "plect.session_source.*"]

[review_agent.event.channel.inputs]
path = { from = "nodes.agent.outputs.socket_path" }

[review_agent.tick]
on        = ["resource.*", "plect.judge.recorded"]
heartbeat = "15m"

[review_dispatch]
kind       = "session_source"
poll_every = "1m"

[review_dispatch.discover_inputs]
repositories = ["example/widgets"]
labels       = ["agent-review"]
state        = "open"
draft        = false

[review_dispatch.session]
workflow     = "review_agent"
task         = "official.github.review"
max_sessions = 8

[review_dispatch.session.inputs]
instruction = "Review the pull request and record the verdict against its current revision."

[review_dispatch.destroy_when]
all = [{ check = "resource.state.lifecycle_state", in = ["closed", "merged"] }]
```

Every reference in the example is static. `official.github.review` exists as
a task written for `official.github.pull`; the ADR adds that observer's
discover and `lifecycle_state` contracts. The workflow, source, and their
inputs are user-owned. `resource.state.lifecycle_state` resolves through the
task to the observer, while the workflow's runtime channel reads an existing
node output root.

#### Operations-chat configuration

This complete user-owned TOML binds the Slack push face above to an operations
task. The triggering `mention_ts` is a typed discovery-item field, and later
inbound thread events extend the idle deadline without a dispatcher process.

```toml
[ops_chat]
kind              = "task"
description       = "Investigate and respond to one operations conversation"
resource_observer = "official.slack.thread_state"
instructions      = [{ text = "Investigate the operations request at {{ resource.id }}, respond in its thread, and link an issue when the work must continue asynchronously." }]

[ops_chat.inputs_schema]
type                 = "object"
additionalProperties = false

[ops_chat.inputs_schema.properties]
slack_base_url = { type = "string" }
mention_ts     = { type = "string" }

[ops_chat_session]
kind               = "workflow"
workspace_provider = "official.slack.thread"

[ops_chat_session.inputs_schema]
type                 = "object"
required             = ["task", "slack_base_url", "mention_ts"]
additionalProperties = false

[ops_chat_session.inputs_schema.properties]
task           = { type = "string", enum = ["ops_chat"] }
slack_base_url = { type = "string", format = "uri" }
mention_ts     = { type = "string" }

[[ops_chat_session.nodes]]
id   = "pane"
uses = "official.tmux.pane"

[[ops_chat_session.nodes]]
id   = "agent"
uses = "official.claude.runtime"

[[ops_chat_session.nodes]]
id   = "thread_delivery"
uses = "official.slack.slack_subscribe"

[ops_chat_session.nodes.inputs]
base_url        = { from = "session.inputs.slack_base_url" }
thread_ts       = { from = "workspace.thread_ts" }
channel_id      = { from = "workspace.channel_id" }
socket_path     = { from = "nodes.agent.outputs.socket_path" }
catch_up_through = { from = "session.inputs.mention_ts" }

[[ops_chat_session.event.channel]]
name    = "runtime"
uses    = "official.claude.delivery"
include = ["plect.instruction", "user.emit"]

[ops_chat_session.event.channel.inputs]
path = { from = "nodes.agent.outputs.socket_path" }

[[ops_chat_session.event.channel]]
name    = "thread"
uses    = "official.slack.slack"
include = ["plect.session_source.*", "plect.channel.error"]

[ops_chat_session.event.channel.inputs]
base_url   = { from = "session.inputs.slack_base_url" }
channel_id = { from = "workspace.channel_id" }
thread_ts  = { from = "workspace.thread_ts" }

[ops_chat_session.tick]
on        = ["user.emit", "resource.*"]
heartbeat = "15m"

[ops_mentions]
kind  = "session_source"
idle  = "8h"
grace = "15m"

[ops_mentions.discover_inputs]
base_url    = "http://127.0.0.1:7890"
channel_ids = ["C01234567"]

[ops_mentions.session]
workflow     = "ops_chat_session"
task         = "ops_chat"
max_sessions = 12

[ops_mentions.session.inputs]
slack_base_url = "http://127.0.0.1:7890"
mention_ts     = { from = "discovery.mention_ts" }

[ops_mentions.keep_while]
all = [{ check = "resource.state.linked_issue_status", in = ["open"] }]
```

The Slack plugin sketches introduce `official.slack.thread_state` and its
`discovery.mention_ts` and `resource.state.linked_issue_status` contracts. All
other references and roots in the example are existing workflow, workspace,
node-output, session-input, task-instruction, effect, and channel surfaces.

### 5. Evaluator placement: the resident process

#### Options

1. Generate shell or Go reconcilers from configuration. This leaves lifecycle
   correctness, provenance, event recording, and retry behavior outside core.
2. Run reconciliation from an ordinary session's tick. This requires a
   bootstrap session, makes source survival depend on the work it supervises,
   and subjects discovered sessions to a parent's `max_up_children` cap.
3. Run one source evaluator in `plect serve`, beside but separate from session
   tick reactors.

#### Recommendation

The evaluator lives in `plect serve`. A poll source receives its own
`poll_every` clock; a push source owns one supervised discover stream. A
successful config reload adds, replaces, or stops evaluator loops. A failed
reload keeps the last valid loops and desired state, matching the resident
process's fail-closed posture.

Each evaluation is plan-then-apply. It computes the complete set of allowed
creates, idempotent ups, and owned destroys before mutating state, then invokes
the existing lifecycle services. Concurrent evaluations of one source are
coalesced. A failed mutation remains visible and is retried on the next poll,
appearance, inbound event, or expiry deadline; it does not roll back a
different session whose lifecycle already completed.

Source-created sessions then use ordinary workflow tick reactors. The source
evaluator does not tick tasks, interpret completion, or deliver notifications.
It records `plect.session_source.*` decision, conflict, and failure events on
an affected session. Workflow `[[event.channel]]` bindings may relay those
events like any other. A discover failure with no owned session is visible in
resident logs; the language gains no destination, message, or notification
field to special-case it.

`session.max_sessions` and workflow `max_up_children` answer different
questions. The former bounds parentless sessions created by one source. The
latter continues to bound concurrently-up children of each source-created
session, including chain-spawned children. Source-owned sessions do not count
against one another's child cap, and chain-owned sessions never count against
the source cap.

Chains and sources share session-name resolution but not ownership. A chain
that reaches a name already owned by a source gets the existing-name conflict
instead of adopting or destroying it, and the source behaves the same when a
chain got there first. This preserves the chain's placement and the source's
destruction guard rather than letting two authorities rewrite one session's
provenance.

## Consequences

Implementation changes the configuration language and therefore requires a
dialect increment, a migration procedure with a backup step, structural schema
updates, and conformance fixtures for every valid, invalid, and boundary case.
The implementation must add `session_source` to definition discovery,
references, trusted-layer rules, status output, and the resident supervisor,
without adding any provider name to core.

The implementation order is:

1. Add poll and push discover faces to the relevant plugin observers,
   including parameter and item schemas. The Slack adapter exposes its
   unbound-mention stream without deciding a workflow.
2. Add language validation and the resident evaluator, including provenance,
   fail-closed planning, admission, expiry, and ordinary session events.
3. Cut downstream deployments over to user-owned `session_source`
   declarations and remove their dispatch loops.
4. Remove the Slack opaque command hook and publish its one-time migration.

After cutover, downstream configuration retains team parameters, workflows,
task specialization, and instructions. Deployment infrastructure retains
credentials, service management, and Terraform. Until the failure-model work
lands, downstream operations may also retain the narrow recovery shim that
detects stale produced state and deliberately rebuilds it. That shim does not
enumerate desired resources or choose sessions; this decision neither absorbs
nor legitimizes its verify-before-skip behavior.

The new surface is falsified before implementation is accepted if prototypes
of the three named consumers cannot share the same discover-item, provenance,
and lifecycle contracts without any of the following:

- provider-specific vocabulary or branching in core;
- dynamic workflow or task selection;
- a cross-resource join or a new predicate language;
- plugin-owned team repositories, channels, limits, or retention values;
- a push mode that must infer absence from stream silence;
- adoption or destruction of sessions lacking source provenance.

If one of those is necessary, the generic kind is not retained merely because
it was designed here. The incompatible consumer stays plugin- or
deployment-local while a narrower language decision is made. If only one
consumer remains after the poll, push, and orchestrator prototypes, the shared
abstraction also loses its justification under the repository's YAGNI rule.

## Alternatives considered

### Keep downstream reconcilers

Deployment scripts can call `plect up` and `plect destroy` today. They cannot
share core's load-time reference validation, schema roots, provenance guard,
resident supervision, or event history, and every deployment must reproduce
the same convergence and failure rules. This is the observed duplication the
decision removes.

### Make discover a new standalone plugin kind

A standalone `resource_discoverer` could be referenced by both observer and
source. No concrete consumer needs discovery independently of a resource
contract, and it would introduce a second reference whose item resource must
still be checked against an observer. Keeping discover as a face of the
observer makes recognition, observation, finalization, and appearance one
contract for one external resource kind.

### Extend workspace providers instead of resource observers

A workspace provider recognizes a resource for session naming and owns
workspace acquisition. Discovery is useful without a workspace and its
lifecycle predicates read observed resource state. Putting discovery on the
provider would reunite responsibilities the language deliberately separates
and would make a provider and observer two authorities for the same resource
kind.

### Treat push appearances as ordinary session events

There is no session log before the appearance creates a session. Inventing a
placeholder session solely to receive that event would make bootstrap identity
and cleanup circular. The discover stream is ingress to desired-state
evaluation; after creation, ordinary inbound events belong to the real
session's durable log and extend its idle deadline.

### Add recovery and verify-before-skip

Reconciliation will call the existing idempotent lifecycle paths, so it will
inherit their current produced-state behavior. Adding validity probes,
degraded run state, cleanup-on-failure, or selective reconstruction here would
mix resource-population policy with the independent failure model and obscure
which authority answered whether a session exists versus whether its runtime
is healthy.
