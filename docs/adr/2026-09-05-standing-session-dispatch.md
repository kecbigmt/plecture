# Standing session dispatch from resource discovery

## Context

Plecture can dispatch one session explicitly and can let a task instance spawn
another session through a chain. It cannot declare a standing population:
for every external resource matching deployment-owned conditions, keep one
session present, and remove that session when the resource no longer matches.

A downstream deployment consequently carries a large reconciliation program
for pull-request review dispatch. A downstream deployment needs the same
policy for operations conversations, but its source is a pushed chat mention
rather than an enumerable query. A downstream deployment needs the enumerable
form for orchestrator polling. These are three concrete consumers of one
missing language concept, not a speculative extension point.

One of those deployments also exhausts root-level run capacity while review
sessions are merely waiting for human replies. Destroying them would release
capacity only by discarding the state, workspace, event log, and thread
continuity that the eventual reply must resume. Desired-set membership and
run-resource occupancy therefore need separate transitions: the member
remains present while its session goes down, then comes up when work returns.

The existing language already separates the responsibilities this feature
must join:

- A resource observer recognizes one resource, observes its live state, and
  declares the `resource.state.*` contract
  ([resource observers](../language/resource-observers.md)).
- A task document declares which observer its work is written for, and its
  completion and chains read the observer's facts
  ([tasks](../language/tasks.md)).
- A workflow declares the user-owned policy, effects, and workspace provider
  that produce a session ([workflows](../language/workflows.md)).
- A chain is colocated with the task whose instance facts it reads, allowing
  its condition, target workflow, and inputs to be validated before it fires
  ([chains](../language/chains.md)).
- Definition blocks, rather than files, carry identity. They share one
  namespace per layer, and topology references are static
  ([declarations](../language/declarations.md)).
- Each value surface exposes only the roots that exist at that point, and live
  `resource.state.*` reads are coherent within one evaluation
  ([values](../language/values.md)).
- The session status command defines an empty message as the runtime's idle
  self-report, but current session state stores both an explicit empty report
  and “never reported” as no message. Durable status-message events already
  record changes; capacity policy needs the first explicit empty report to be
  recorded as well.

The missing construct is deployment policy. A plugin can say how resources
are discovered, but it cannot choose a team's repositories, labels, chat
channels, workflow, task, concurrency, or expiry policy. The owner-ratified
[exemplar-workflow direction](https://github.com/kecbigmt/plecture/issues/155)
says running workflows are user-owned and catalogs ship copy-templates rather
than mounted workflow definitions. Its formal language change is still
pending; this decision depends on and enforces the same premise that workflows
are policy.

This decision concerns desired session presence. It does not change whether a
persisted node is still valid, whether `plect up` verifies before skipping it,
or how a partially failed run is represented. Those recovery questions remain
the responsibility of [the failure-model work](https://github.com/kecbigmt/plecture/issues/371).

## Decision

### 1. Placement: nest populations in the user-owned workflow

#### Options

| Option | Load-time validation | Layer and cascade owner | Plugin-boundary consequence | Assessment |
|---|---|---|---|---|
| (a) New top-level kind | The observer, optional initial task, target workflow, both input contracts, and item roots resolve from one definition. | A trusted layer owns a separate whole definition and a static workflow reference. | Plugin mechanics and deployment values remain separated, but population policy gets an identity outside the user policy it activates. | Viable, but unnecessary once a running workflow is user-owned; it adds a kind and a second policy owner for no additional validation. |
| (b) Array nested in `workflow` | The target workflow and its input contract are local; observer, task, discover, and item references still resolve statically. | The user-owned workflow owns its complete population array. A deeper eligible workflow layer replaces that array wholesale, in the same cascade family as `[tick]` rather than additive `nodes`. | Catalogs may show commented scaffold placeholders, but mounted plugin content cannot activate them. Team parameters remain in the user's copy. | Recommended: a standing population is policy for producing sessions of this workflow, which is exactly the responsibility the user-owned workflow already carries. |
| (c) Nested in `task` beside chains | The observer facts are local and a target workflow can be resolved like a chain. | Task `extends` is additive, so population policy would compose through task specialization even though only one deployment should own a population. | Shipped tasks would either contain team data or require a user to replace or extend plugin work merely to choose deployment policy. | Rejected: chains fire from an existing instance, while discovery fires before an instance exists and must also support sessions that intentionally start without a task. |
| (d) Added to `config.toml` | References and schemas could be checked, but the entry would have no definition identity of its own. | The reserved file is machine-wide resolution and defaults, outside definition discovery and cascade. | It would centralize every deployment rule in a reserved file and provide no address for provenance or events. | Rejected: it violates the definition-block principle and cannot name the authority allowed to destroy a session. |
| (e) Nested in `resource_observer` | Discovery and item schemas would be colocated. The workflow and task could also be resolved. | Resource observers are whole-definition plugin resources; a user cannot append policy to one and replacing one copies plugin mechanics into the deployment layer. | Team repositories, channels, limits, and workflow choices would land in plugin-owned files or force a fork. | Rejected: the observer should gain the reusable discovery mechanism, not deployment policy that consumes it. |

#### Recommendation

Use a named array of tables on `workflow`. A workflow may declare zero or more
population entries. The workflow reference carried by the former top-level
shape disappears: the containing workflow is the target, so there is no
second reference that can disagree with it.

Only user- and machine-owned trusted workflow layers may declare the array. A
declaration in mounted plugin content is a load error even though the exemplar
direction removes that activation vector; a declaration in the untrusted
workspace overlay is also a load error. An exemplar may carry a commented
placeholder, and scaffolding verifies its references and required parameters
when it copies the workflow into user ownership.

Across eligible workflow cascade layers, omission inherits the shallower
array and declaration replaces the whole array. Entries never append across
layers: partial composition could retain a shallower deployment policy the
deeper owner believed it replaced. Within one definition layer, the ordinary
cross-file array rule still appends entries in traversal order. Entry names
must be unique after that same-layer assembly.

Each entry requires a `name` matching the definition-id grammar. The creator
provenance persisted on every session is the workflow id (its fully resolved
address) plus the entry's required `name`, which serves as its block name. A
deeper whole-array replacement may keep ownership by retaining the name; a
differently named entry cannot adopt or destroy those sessions. An invalid
reload keeps the last valid evaluator. Clean removal of an entry stops its
evaluator without destroying its sessions, because removal
of policy is not evidence that a resource left a successfully observed set;
the operator can destroy the provenance-marked sessions explicitly or first
replace the entry with parameters whose successful snapshot is empty.

One bootstrap constraint remains: an active population must come from the
resident process's workspace-independent trusted workflow load. A workflow
fragment discoverable only after a resource workspace exists cannot decide
whether that first workspace should be created; such a population declaration
is a load error rather than inert configuration. This constraint would apply
to any placement, including a top-level definition, so it is not a reason to
retain the extra kind.

No load-bearing reason for the top-level kind survives these identity, trust,
cascade, and bootstrap rules. Workflow nesting retains complete load-time
validation while matching the ratified policy owner.

### 2. Population block name: `populations`

The collection field and each entry's required `name` are distinct. This
decision names the array below the workflow; the entry name is the stable block
identity used with the workflow id for provenance.

| Candidate | Discovery face | Lifecycle face | Language fit |
|---|---|---|---|
| `sources` | Clearly says where sessions come from. | Says little about continued membership, expiry, or teardown. | Plural agrees with `nodes` and `chains`, but keeps the misleading one-way emphasis. |
| `triggers` | Clearly suggests an event or condition starts work. | Implies one-shot firing rather than reconciliation and destruction. | Familiar, but wrong for poll snapshots and ongoing ownership. |
| `population` | Names the desired set produced by one entry. | Membership naturally includes both presence and removal. | Precise, but singular is inconsistent with array field names. |
| `populations` | Names the desired sets the workflow maintains. | Membership naturally covers admission, retention, and removal. | Matches the plural array convention of `nodes`, `chains`, and `instructions`. |
| `presence` | Emphasizes the desired-state guarantee. | Covers keeping and removing sessions, but underplays discovery and admission. | A good semantic noun, though several entries read less naturally as one `presence` array. |
| `standing` | Suggests continuous operation. | Implies duration without saying what is maintained. | An adjective, unlike the workflow's noun field vocabulary. |
| `roster` | Suggests a membership set. | Can imply addition and removal. | Reads as a manually curated list rather than dynamic discovery. |
| `intake` | Expresses admission from an external source. | Underweights retention and teardown. | A process noun, but still one-way. |
| `lifecycle` | Expresses retention and teardown directly. | Underweights discovery and the desired set. | Overlaps effect lifecycle while governing sessions rather than workflow nodes. |
| `reconciliation` | Covers convergence of membership in both directions. | Makes creation and destruction equally visible. | Names the evaluator mechanism rather than the declarative thing being maintained. |

Use `populations`. Each
`[[<workflow>.populations]]` entry declares one desired population, and its
required `name` identifies that entry for provenance. The plural spelling
follows the language's array-field convention, while “population” covers both
the discover/admit face and the retain/remove face without naming evaluator
mechanics.

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
| `wake` | Optional action for poll; forbidden for push. | Supervises a hint stream that requests an immediate complete snapshot, as specified in Decision 6. |

The action's `inputs.*` root is the population entry's literal
`discover_inputs` object, validated against `inputs_schema` before the action
runs. No session, workspace, node, or resource exists yet, so no other data
root is exposed. Capabilities remain available only in the action positions
that already accept them.

A poll action runs to completion and writes one JSON array. That array is the
complete current membership set for those parameters; successful exit means
pagination and every item enrichment are complete. If metadata for one
matching resource cannot be fetched, the action must fail instead of omitting
that resource or returning a partial record. A push action is a supervised
stream and writes one JSON object per line as resources appear. Stream
termination or a non-zero exit is a discovery failure and is restarted with
the resident supervisor's bounded backoff. It never implies that any resource
disappeared.

Mode describes whether membership in the resource set can be enumerated, not
how a provider receives notifications. An enumerable pull request remains
`poll` even when a webhook reduces its discovery latency. An unbound chat
mention remains `push` whether its adapter receives Socket Mode events or an
Events API webhook; that transport choice belongs to the adapter service and
is invisible to the language. Choosing `push` for an enumerable resource
would discard the complete snapshot that repairs missed notifications and
proves absence.

Both outputs validate each object against `item_schema`. Within a population
entry's session input, the item's declared properties are available as
`discovery.*`, while its required `resource` property also becomes the
`resource.id` passed to session dispatch. These are per-item production
records, not live roots. They are evaluated when a session is first created;
a later discovery of the same resource does not mutate frozen session inputs.

A population entry's required static `resource_observer` reference is the
single authority for resource recognition, discovery, and live resource facts.
The loader resolves it and requires it to declare `discover`. When the
entry also declares an initial `session.task`, the loader requires that task's
observer to resolve to the exact same definition. The two references answer
different questions—what population is discovered and what work starts—but
their load-time equality prevents them from becoming competing resource
authorities.

For each evaluation, core first validates the action result, all items,
resource matches, workflow dispatch, and input bindings. A malformed item,
duplicate resource, incomplete or failed action, unresolved root, observer
mismatch, or workspace-provider mismatch fails the whole evaluation before any
create or destroy. A failing poll therefore proves neither presence nor
absence. One bad push item fails only that item's evaluation; it does not
retract earlier appearances.

#### Poll discover sketch

The GitHub plugin extends its existing pull-request observer with a complete,
enumerable search. Filtering `inputs.state` in the query makes that snapshot
the authority for both presence and absence.

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

[pull.discover.wake]
type = "exec"
bin  = "github-webhook-receiver"
args = [
  "stream-hints",
  "--repositories", { json = { from = "inputs.repositories" } },
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
required             = ["resource", "owner", "repository", "title", "head_sha"]
additionalProperties = false

[pull.discover.item_schema.properties]
resource   = { type = "string", format = "uri" }
owner      = { type = "string" }
repository = { type = "string" }
title      = { type = "string" }
head_sha   = { type = "string" }

[pull.state_schema]
type     = "object"
required = ["resource_kind", "checks_status", "revision", "pr_url", "mergeable_state", "review_decision"]

[pull.state_schema.properties]
resource_kind   = { type = "string", enum = ["pull"] }
checks_status   = { type = "string", enum = ["SUCCESS", "PENDING", "FAILURE", "NULL"] }
revision        = { type = "string" }
pr_url          = { type = "string" }
mergeable_state = { type = "string", enum = ["clean", "dirty", "unstable", "blocked", "behind", "unknown", "draft", "has_hooks"] }
review_decision = { type = "string", enum = ["APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", "NULL"] }
```

The wake executable is introduced with this discover face. It connects to a
webhook-receiver service and streams hints; it does not emit pull-request
records. Exposing the webhook endpoint and verifying request signatures are
deployment-infrastructure responsibilities outside the configuration
language.

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
additionalProperties = false
```

The adapter's
[`on_unbound_mention`](https://github.com/kecbigmt/plecture/issues/362) hook is
subsumed, not retained as a second dispatch authority. During rollout it can
feed the discover stream while the generic contract is proved, but the
supported end state has the adapter expose the appearance stream directly and
removes the opaque command hook with the required one-time migration. Keeping
the hook indefinitely would leave two authorities deciding whether one
mention creates one session.

### 4. Session surface and built-in membership lifecycle

#### Options

1. Let the discover action return complete `plect up` commands. This puts
   workflow and deployment policy in executable output and makes topology
   dynamic.
2. Give each population configurable predicates for destruction and up/down
   transitions. This is expressive, but makes every deployment restate safety
   and capacity policy that the evaluator can enforce uniformly.
3. Keep session topology static and make membership lifecycle a closed part of
   poll and push reconciliation.

#### Recommendation

Use option 3. This decision makes no change to the shared fact grammar.
`done_when` and chain `when` retain their existing conjunction-only shape,
task extension composition is unchanged, and population entries contain no
lifecycle predicates.

The closed workflow-population surface is:

| Field | Requirement | Meaning |
|---|---|---|
| `populations.name` | Required identifier, unique within the workflow. | Gives this entry stable provenance below the workflow address. |
| `populations.resource_observer` | Required static resource-observer reference. | Selects the sole recognition, discovery, and population-resource observation authority. |
| `populations.discover_inputs` | Required table. | Literal deployment data validated against the observer's discover `inputs_schema`. |
| `populations.session.task` | Optional static task reference. | Selects a task to set up after the session is up; its observer must equal `resource_observer`. |
| `populations.session.inputs` | Optional value table. | Session inputs over literals, `resource.id`, and the discover `item_schema`'s `discovery.*` properties. |
| `populations.session.destroy.force` | Optional boolean; default false. | Uses the lifecycle service's explicit force-destroy path for entry-owned sessions. |
| `populations.poll_every` | Required positive duration for poll; forbidden for push. | Sets complete-snapshot cadence. |
| `populations.expire_after` | Required positive duration for push; forbidden for poll. | Expires a push appearance after that duration of external-input quiescence, subject to the task guard below. The spelling is recommended pending the ruling below. |
| `populations.auto_down` | Optional boolean; default false. | When true, allows the evaluator to select this entry's explicitly idle sessions under root-cap pressure. |
| `populations.auto_destroy` | Optional boolean; default false. | When true, allows the evaluator to execute an otherwise eligible guarded destruction. False retains the session and emits the same decision as a dry-run event. |

The complete `session.inputs` object is validated against the target
workflow's `inputs_schema` at load time: literal types are checked directly,
`resource.id` is a string, and every `discovery.*` path is resolved against
the observer's discover `item_schema`. Runtime validation still checks schema
constraints that static projection cannot prove. When `session.task` is
present, every task input not supplied explicitly is bound from session inputs
by the existing dynamic task-setup rules, and each common field must also
satisfy the task's `inputs_schema`.

`resource_observer` and `session.task` are static topology. They accept the
ordinary relative or catalog-qualified reference grammar and cannot be CEL
expressions. The containing workflow's workspace provider and population
observer must both match each discovered `resource`; the provider remains the
single authority for session naming.

#### Push-expiry field name: owner ruling required

| Candidate | Strength | Risk |
|---|---|---|
| `expire` | Shortest and economical. | Reads like an imperative or an absolute timestamp rather than a duration. |
| `expire_after` | Makes the duration shape and eventual destruction explicit. | The activity origin must still be defined by the language. |
| `idle` | Short and familiar for elapsed inactivity. | Conflicts with the runtime's empty status message and health-probe terminology, which have different authorities. |

Recommend `expire_after`. It is longer than `expire`, but says that the
value is a duration and avoids overloading “idle.” The clock starts at session
creation and is reset to the latest accepted repeated push appearance or event
recorded on the session log with `direction = "inbound"`. Internal ticks,
population and lifecycle events, outbound agent events, and session status
messages do not reset it. Time spent as a pending appearance before creation
does not count. This measures external-input quiescence; it is independent of
the runtime's explicit empty status message used for capacity decisions and a
health probe's `silence_expected` turn-boundary exception.

#### Automatic-action control shape: owner ruling required

Independent down and destroy controls are required, and both default off. Their
spelling still needs confirmation. Three compact shapes are available:

| Shape | Strength | Cost |
|---|---|---|
| Booleans `auto_down` / `auto_destroy` | Directly represents the two independent permissions; omission safely denies each action. | Steady-state configuration must opt in with two explicit true values. |
| Strings `down = "auto"` or `"manual"`, and likewise for destroy | Each value reads positively and can gain modes later. | Adds enum vocabulary when only permission is demonstrated. |
| One combined mode | Uses one field. | Needs four combinations and couples permissions the owner requires independently. |

Recommend the two booleans. They add only the two decisions that exist and do
not speculate about additional modes. Both default to false: a freshly declared
population discovers, creates, brings up, and sets up initial tasks, but it
neither takes sessions down nor destroys them until the operator enables each
action explicitly. This is a deliberate safety bias for rollout, not full
convergence: absent or expired sessions produce dry-run destruction verdicts,
and explicitly idle sessions remain up and consume root capacity until the
corresponding switch is enabled or the operator acts manually. Steady-state
configuration sets both values to true.

Poll membership is exactly the latest successful complete snapshot. For an
owned member missing from that snapshot, the evaluator plans destruction; the
discover query itself expresses exclusions such as `state = "open"`. A
failed or partial query proves no absence, and a present item cannot request
destruction. No per-entry field supplies a second removal authority.

Push has no absence proof. Its appearance generation becomes eligible for
destruction only when `expire_after` matures. A repeat appearance resets the
clock and an inbound event on the session does the same. Successful destruction
closes that generation; a later accepted appearance starts a new one.

Before either poll-absence or push-expiry destruction, the evaluator checks
every dynamic task instance owned by the session through the task's existing
completion semantics. It never automatically destroys while any instance is
not satisfied. Missing completion policy, an observation or expression error,
and an unobserved or pending result all count as unsatisfied. Deferral records
a `plect.workflow_population.destroy_deferred` event on the member, including
the population provenance and blocking task instances. The evaluator records
the first deferral and any change to its blocker set rather than repeating the
same event every cycle. A blocking task-state or completion-result change wakes
the deferred decision; observation failures retain it under bounded retry, so
a push member does not require another appearance merely to finish deferred
expiry. Manual `plect destroy` is outside this population guard and retains its
ordinary cleanup and force rules. Consequently a task that can never become
satisfied prevents automatic expiry forever; the operator must resolve it
explicitly or fix the task's own completion design.

With `auto_destroy = false`, the evaluator still computes snapshot absence or
push expiry and runs the built-in task guard. An unsatisfied task records the
same deferral. Once the guard is clear, it records
`plect.workflow_population.destroy_dry_run` with the source reason and
provenance instead of calling the lifecycle service. Repeated identical
evaluations do not duplicate that event; a changed verdict, blocker set, or
discovery generation does. Turning `auto_destroy` on triggers re-evaluation.
No destruction tombstone is written until a destroy is actually attempted.

The evaluator persists a discovery-generation tombstone before completing an
owned destruction. For poll, the successful absent generation remains closed
until a later successful snapshot contains the resource. For push, only a later
accepted appearance opens a new generation. The tombstone prevents stale or
concurrent work from recreating an expired member and contains only source
identity, provenance, and generation state, not destroyed session state.

A valid push appearance is persisted before admission. If root capacity is
full, it remains pending and is admitted in resource-id order when capacity
becomes available; process restart does not forget an accepted appearance. A
repeated appearance replaces the pending production record. Its expiry clock
begins only when the session is created.

Creation, up, down, and destruction use the same service paths as `plect up`,
`plect down`, and `plect destroy`, including resource allowlists,
workspace-provider resolution, cleanup, and errors. `session.destroy.force`
is an explicit choice to pass the same force option; discovery itself never
silently weakens cleanup guards. A population persists its containing workflow
address and entry name on sessions it creates and may mutate only those
sessions. It never adopts an existing session with the same derived name. A
population/chain or population/population name collision records a conflict and
leaves the existing session untouched. Manual `plect down`, `plect up`, and
`plect destroy` remain available regardless of `auto_down` and `auto_destroy`;
the switches constrain only evaluator-initiated actions.

When `session.task` is present, a successful `up` is followed by the
existing task-setup service with the fixed instance name `initial` and the
discovered `resource`. The evaluator first reads session state: an `initial`
instance with the exact resolved task and resource is already converged; a
missing one is set up; and any conflicting `initial` instance fails closed.
The population does not use `plect up --task`, because that flag is only
workflow-input shorthand and does not instantiate a task by itself. A
population with no `session.task` starts only its containing workflow and may
receive dynamically created tasks later through the ordinary task-setup path.

#### Review-dispatch configuration

The machine's reserved configuration declares the one cap for every session
whose logical parent is the virtual root:

```toml
# config.toml excerpt
max_up_children = 8
```

This complete user-owned workflow TOML binds the GitHub poll face above to an
existing review task. Together they replace an external reconcile loop.

```toml
[review_agent]
kind               = "workflow"
workspace_provider = "official.github.worktree"

[review_agent.inputs_schema]
type                 = "object"
required             = ["app_id", "private_key_path", "slack_base_url", "slack_channel_id", "owner", "repo", "pr_title", "pr_url", "head_sha"]
additionalProperties = false

[review_agent.inputs_schema.properties]
app_id           = { type = "string", pattern = "^[0-9]+$" }
private_key_path = { type = "string", pattern = "^[A-Za-z0-9_./:-]+$" }
slack_base_url   = { type = "string", format = "uri" }
slack_channel_id = { type = "string" }
owner            = { type = "string", pattern = "^[A-Za-z0-9-]+$" }
repo             = { type = "string", pattern = "^[A-Za-z0-9._-]+$" }
pr_title         = { type = "string" }
pr_url           = { type = "string", format = "uri" }
head_sha         = { type = "string" }
instruction      = { type = "string", default = "" }

[[review_agent.nodes]]
uses = "official.tmux.pane"

[[review_agent.nodes]]
id   = "gh_app_guard"
uses = "official.github.gh_app_guard"

[review_agent.nodes.inputs]
app_id           = { from = "session.inputs.app_id" }
owner            = { from = "session.inputs.owner" }
repo             = { from = "session.inputs.repo" }
private_key_path = { from = "session.inputs.private_key_path" }

[[review_agent.nodes]]
id   = "agent"
uses = "official.claude.runtime"

[review_agent.nodes.inputs]
path_prepend = { from = "nodes.gh_app_guard.outputs.dir" }

[[review_agent.nodes]]
id   = "slack_thread"
uses = "official.slack.slack_thread"

[review_agent.nodes.inputs]
base_url   = { from = "session.inputs.slack_base_url" }
channel_id = { from = "session.inputs.slack_channel_id" }
root_text  = { expr = "'[AI review] ' + session.inputs.pr_title + ' — ' + session.inputs.pr_url + '\\nhead ' + session.inputs.head_sha" }

[[review_agent.nodes]]
uses = "official.slack.slack_subscribe"

[review_agent.nodes.inputs]
base_url   = { from = "session.inputs.slack_base_url" }
thread_ts  = { from = "nodes.slack_thread.outputs.thread_ts" }
channel_id = { from = "nodes.slack_thread.outputs.channel_id" }
socket_path = { from = "nodes.agent.outputs.socket_path" }

[[review_agent.event.channel]]
name    = "runtime"
uses    = "official.claude.delivery"
include = ["plect.instruction", "user.emit", "plect.workflow_population.*"]

[review_agent.event.channel.inputs]
path = { from = "nodes.agent.outputs.socket_path" }

[[review_agent.event.channel]]
name    = "review_thread"
uses    = "official.slack.slack"
include = ["plect.judge.recorded", "plect.workflow_population.*", "plect.channel.error"]

[review_agent.event.channel.inputs]
base_url   = { from = "session.inputs.slack_base_url" }
channel_id = { from = "nodes.slack_thread.outputs.channel_id" }
thread_ts  = { from = "nodes.slack_thread.outputs.thread_ts" }

[review_agent.tick]
on        = ["resource.*", "plect.judge.recorded"]
heartbeat = "15m"

[[review_agent.populations]]
name              = "review_dispatch"
resource_observer = "official.github.pull"
poll_every        = "1m"
auto_down         = true
auto_destroy      = true

[review_agent.populations.discover_inputs]
repositories = ["example/widgets"]
labels       = ["agent-review"]
state        = "open"
draft        = false

[review_agent.populations.session]
task = "official.github.review"

[review_agent.populations.session.destroy]
force = true

[review_agent.populations.session.inputs]
app_id           = "123456"
private_key_path = "/etc/plect/github-app.pem"
slack_base_url   = "http://127.0.0.1:7890"
slack_channel_id = "C01234567"
owner            = { from = "discovery.owner" }
repo             = { from = "discovery.repository" }
pr_title         = { from = "discovery.title" }
pr_url           = { from = "resource.id" }
head_sha         = { from = "discovery.head_sha" }
instruction      = "Review the pull request and record the verdict against its current revision."
```

Every reference in the example is static. `official.github.review` exists as
a task written for `official.github.pull`; the ADR adds that observer's
discover contract. The workflow, population, credentials, team parameters,
and instructions are user-owned. Each `discovery.*` path is declared by the
item schema, and every workflow root and plugin definition already exists. The
initial task receives its declared `app_id`, `owner`, `repo`,
`private_key_path`, and `instruction` inputs from the session after `up`. The
discover query's `state = "open"` is the removal policy: closed or merged pull
requests leave the next successful snapshot. When root capacity is contended,
a review session waiting for a reply can go down after its runtime explicitly
reports idle, freeing a slot for a newly discovered pull request. The reply is
an inbound session event, so it requests that retained member come back up; if
the cap is still full, that request remains pending.
The explicit true settings make this the steady-state, fully convergent form.
A fresh rollout can omit both switches: matching resources are still admitted,
but teardown remains a dry-run and sessions remain up under cap pressure until
the operator enables each action.

#### Operations-chat configuration

This complete user-owned TOML binds the Slack push face above to an operations
workflow. The triggering `mention_ts` is a typed discovery-item field, and
later inbound thread events reset push expiry without a dispatcher process.
The session intentionally starts without a task.

```toml
[ops_chat_session]
kind               = "workflow"
workspace_provider = "official.slack.thread"

[ops_chat_session.inputs_schema]
type                 = "object"
required             = ["slack_base_url", "mention_ts"]
additionalProperties = false

[ops_chat_session.inputs_schema.properties]
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
include = ["plect.workflow_population.*", "plect.channel.error"]

[ops_chat_session.event.channel.inputs]
base_url   = { from = "session.inputs.slack_base_url" }
channel_id = { from = "workspace.channel_id" }
thread_ts  = { from = "workspace.thread_ts" }

[ops_chat_session.tick]
on        = ["user.emit", "resource.*"]
heartbeat = "15m"

[[ops_chat_session.populations]]
name              = "ops_mentions"
resource_observer = "official.slack.thread_state"
expire_after      = "8h"
auto_down         = true
auto_destroy      = true

[ops_chat_session.populations.discover_inputs]
base_url    = "http://127.0.0.1:7890"
channel_ids = ["C01234567"]

[ops_chat_session.populations.session.inputs]
slack_base_url = "http://127.0.0.1:7890"
mention_ts     = { from = "discovery.mention_ts" }
```

The Slack plugin sketch introduces `official.slack.thread_state` and its
`discovery.mention_ts` contract. All other references and roots are existing
workflow, workspace, node-output, session-input, task, effect, and channel
surfaces. An explicit escalation may later create an
`official.github.investigate` instance, bound to an issue resource, through
the ordinary dynamic task-setup path. Its observer declares
`resource.state.issue_status` as `PENDING` for open and `SUCCESS` for closed.
After eight hours without a repeated appearance or inbound event, push expiry
makes the session eligible for destruction. The built-in task guard defers that
destruction with zero population configuration while an escalated
investigation is unsatisfied; a failed observation is also unsatisfied and
therefore fails closed. Once every owned task instance is satisfied, the next
evaluation may destroy the expired session.
The explicit true settings make this the steady-state form; omitting them keeps
automatic down and destruction disabled during rollout.

### 5. Evaluator placement: the resident process

#### Options

1. Generate shell or Go reconcilers from configuration. This leaves lifecycle
   correctness, provenance, event recording, and retry behavior outside core.
2. Run reconciliation from an ordinary session's tick. This requires a
   bootstrap session, makes population survival depend on the work it supervises,
   and subjects discovered sessions to a parent's `max_up_children` cap.
3. Run one evaluator per workflow population in `plect serve`, beside but
   separate from session tick reactors.

#### Recommendation

The evaluator lives in `plect serve`. A poll population receives its own
`poll_every` clock and, when its observer declares one, its own parameterized
wake stream; a push population owns one supervised discover stream. A
successful config reload adds, replaces, or stops evaluator loops. A failed
reload keeps the last valid loops and desired state, matching the resident
process's fail-closed posture.

Each evaluation is plan-then-apply for every source and task fact that exists
at its start. It computes membership creates, initial task setups, and guarded
owned destroys before mutating state, then invokes the existing lifecycle and
task-setup services. Up requests enter the serialized root-cap coordinator;
because concurrent admissions and down failures can change capacity, that
coordinator selects one eligible down candidate and retries admission rather
than pretending the complete down set was knowable in the source plan. For an
absent or expired member the evaluator never sets up a task before deciding
whether destruction is guarded. Concurrent evaluations of one population are
coalesced. A failed mutation remains visible and is retried on the next poll,
appearance, inbound event, expiry deadline, or capacity change; it does not
stop or roll back a different session whose lifecycle already completed.

Population-created sessions then use ordinary workflow tick reactors. The
population evaluator does not tick tasks or deliver notifications. For the
destruction guard it invokes the same completion-evaluation path as the
ordinary task evaluator rather than defining another interpretation. It records
`plect.workflow_population.*` decision, deferral, conflict, and failure events
on an affected session. Workflow `[[event.channel]]` bindings may relay those
events like any other. A discover failure with no owned session is visible in
resident logs; the language gains no destination, message, or notification
field to special-case it.

`max_up_children` is the only concurrency vocabulary. A real session's
workflow continues to bound its direct children, including chain-spawned
children. The virtual-root form in Decision 7 bounds all root-level sessions,
including population-created and manually dispatched sessions, without
introducing a population-specific counter. A session counts only against its
logical immediate parent's cap, so a real child is not also counted at the
virtual root.

Chains and populations share session-name resolution but not ownership. A
chain that reaches a name already owned by a population gets the existing-name
conflict instead of adopting or destroying it, and the population behaves the
same when a chain got there first. This preserves the chain's placement and
the population's destruction guard rather than letting two authorities
rewrite one session's provenance.

### 6. Wake streams for poll populations

#### Options

1. Reduce `poll_every` until creation latency is acceptable. This preserves
   correctness but converts a burst-latency requirement into continuous API
   load, and still cannot provide a prompt reaction without aggressive
   polling.
2. Model an enumerable resource as `push` when webhooks are available. This
   treats transport as membership semantics: one missed delivery can prevent
   creation forever, and the evaluator loses snapshot absence as a destruction
   signal.
3. Keep the complete poll snapshot authoritative and let an optional stream
   wake that snapshot early.

#### Recommendation

A poll discover may additionally declare
`[<observer>.discover.wake]`. It is an action with the same supervised-stream
execution shape as a push discover action, but not the same data contract. The
wake action has no `item_schema`; each complete output line is an untrusted
hint whose bytes are discarded. A hint cannot name a resource, supply
`discovery.*` values, admit a session, or destroy one. It only requests that
each population using this observer run its ordinary complete snapshot now.

The wake action reads the same `inputs.*` root as the poll action, backed by
the population's one `discover_inputs` object and validated once against the
discover `inputs_schema`. A second parameter surface is rejected because the
wake and snapshot are two timing faces of the same query, not independently
selectable populations.

The evaluator maintains one pending-wake latch per population. Hints that
arrive before an evaluation starts collapse into one immediate poll. A hint received
while that poll is running requests at most one immediate follow-up; it does
not start a concurrent evaluation. A scheduled `poll_every` tick coalesces
with the same latch. The resulting snapshot follows the full validation,
plan, and apply rules from Decisions 3–5.

Wake stream termination and restart use the same supervised bounded-backoff
rules as a push discover stream. Stream failure does not change desired
membership and never implies absence. `poll_every` remains required and is
the recovery floor, so delayed, duplicated, reordered, or at-most-once webhook
delivery can affect latency but not eventual correctness.

### 7. Capacity-driven down/up and virtual-root admission

Desired membership and run-resource occupancy are separate. Poll absence or
push expiry decides whether a member remains; neither deployment predicates nor
a new lifecycle state decide whether a retained member is up. The evaluator
uses ordinary `plect down` and `plect up` only to reclaim and refill contended
root capacity.

The session forest is completed as one tree with a non-addressable virtual
root. Every session with no real session parent is logically its direct child,
whether created by a population, dispatched manually, or placed beside a
parentless session through the existing `root:<session>` marker. The virtual
root has no workflow, workspace, resource, event log, or lifecycle operation;
it is the structural parent needed to apply the ordinary child-cap rule at the
top of the tree. Parentless sessions may continue to store an empty
`ParentSession`; the virtual edge is derived rather than persisted.

The virtual root must not turn independent root-level sessions into trusted
reviewers or lifecycle actors for one another. The existing `root:<session>`
marker continues to record an explicit sibling cohort for relation, judge, and
terminal-delivery semantics, while resolving to the virtual root for capacity
accounting. Two independently created root-level sessions therefore remain
unrelated for authority even though both consume the virtual root's
direct-child capacity.

#### Capacity options

1. Apply the virtual-root cap only to evaluator-driven up operations. Manual
   dispatch would retain its current freedom, but could silently bypass the
   same host constraint the evaluator must respect.
2. Apply it to every up of a logical virtual-root child, including manual
   `plect up`. This changes manual behavior when an operator has explicitly
   configured a cap, but leaves one admission authority and counting rule.
3. Also add per-population caps for allocation fairness. This could reserve or
   divide root capacity, but no observed contention between populations defines
   the intended allocation policy.

#### Capacity recommendation

Use option 2. The virtual root's optional positive `max_up_children` is
declared in `config.toml`, because machine-wide resolution and defaults are
the only existing owner for a root that has no workflow. Unset retains
unlimited parentless ups. A real parent's cap remains on that parent's workflow
and is unchanged. Each session counts only against its logical immediate
parent's cap, so real children are not also counted at the virtual root.

The documented counting rule is unchanged: a child counts while its run state
is up, an in-flight up admission also counts, and an idempotent up of an
already-up child is exempt. `--force-recreate` still holds an admission because
it first tears run state down. A successful down immediately frees a slot.
Reducing a cap below its current up count selects no victims by itself; a later
population admission can invoke the pressure policy below.

Manual `plect up` at the configured virtual-root cap returns the same cap
error as an up under a capped real parent. The cap still applies to the manual
path, but an operator command does not authorize the evaluator to take a
different session down. The operator may take another root session down, raise
the machine setting, and retry. Population evaluators persist desired creates
and re-ups and may reclaim capacity only from population-owned sessions.

#### Capacity-pressure policy

When an evaluator's atomic virtual-root admission is rejected at the cap, the
resident root-cap coordinator considers up, parentless, population-owned
members across entries whose `auto_down` is true. A member is eligible to go
down only after its runtime has explicitly reported idle by clearing the
session status message.
The durable latest `plect.status_message` event must therefore exist and have
`cleared = "true"`; an absent event is “never reported,” not idle. A runtime
that never reports idle is never selected, which is the safe default. The
status write path emits that clear event for a session's first explicit empty
report even when current state already stores no message; later identical
empty reports remain idempotent.

An accepted repeated appearance, an inbound session event, or a successful
poll observation with changed resource facts invalidates an earlier idle
report. A non-empty status report also supersedes it. The member cannot be
selected again until the runtime emits a later explicit clear. For ordering,
the coordinator defines last activity as the latest of session creation,
accepted re-appearance, inbound event, changed-resource observation, and
status-message event. It sorts eligible members by oldest last activity, with
session name as the stable tie-breaker.

The coordinator takes one candidate down through the ordinary lifecycle
service, retries the pending atomic admission, and repeats only until that
admission succeeds or no eligible candidate remains. A down failure records a
population failure event, leaves that candidate's actual run state
authoritative, and lets the coordinator try the next eligible candidate. It
never selects a manually created session or a session owned by a different
lifecycle authority merely because both count at the virtual root.

With `auto_down = false`, the entry's sessions are omitted from the candidate
set and continue to count while up. The evaluator records that no permitted
candidate was available when this prevents an admission; it does not treat the
switch as a lifecycle failure. Manual `plect down` remains available and frees
capacity normally.

A successful down frees root capacity while preserving population membership.
It runs ordinary run-scoped cleanup and retains the session, workspace, event
log, session-scoped state, dynamic task instances, and population provenance.
The Slack subscription cleanup tombstones its delivery watermark, so the next
up restores it without redelivering the transcript. A later up idempotently
recreates run-scoped nodes such as the agent, pane, and credential guard.
Whether produced state that claims to be live is actually healthy remains the
independent failure-model question in
[issue #371](https://github.com/kecbigmt/plecture/issues/371).

A retained down member becomes an up candidate on exactly these signals:

- an accepted repeated push appearance;
- an event appended to its log with `direction = "inbound"`; or
- a successful scheduled or wake-requested poll that still contains the
  resource and whose validated `resource.state` differs from the last
  successfully persisted state for that member.

The evaluator compares a canonical representation of the complete validated
observer state, not selected provider fields; a failed observation neither
changes the persisted comparison value nor requests up. An unchanged poll
snapshot does not wake a down member. Each wake signal invalidates earlier idle
evidence before requesting ordinary `plect up`, preventing an immediate
down/up cycle until the resumed runtime explicitly clears its status again.

If the cap remains full, the evaluator persists the up request. Owned down
members take available slots before never-created members; within each class,
resource id gives deterministic order. Atomic admissions preserve the shared
cap when evaluators and manual commands race. No cross-population fairness or
reservation is promised. Per-population fairness caps can be added as a pure
extension if observed saturation later defines the allocation policy.

There is no separate bound on total retained membership. Such a bound would
require a destructive eviction policy and could discard the continuity this
decision preserves. Poll snapshot absence and guarded push expiry are the only
automatic membership-removal paths.

This policy has no time-based grace. Capacity pressure is required before a
session goes down, and an explicit idle report is consumed by any later wake
signal. Those two edges provide hysteresis without another configuration
field. If a runtime repeatedly clears immediately after every up, it is
truthfully volunteering the session as the next oldest candidate; a concrete
need for minimum residency or another neutral band can add such a guard later.

## Consequences

Implementation changes the configuration language and therefore requires a
dialect increment, a migration procedure with a backup step, structural schema
updates, and conformance fixtures for every valid, invalid, and boundary case.
It adds the workflow `populations` array, observer discover faces and wake
action, the selected push-expiry duration, the two automatic-action
permissions, and virtual-root `max_up_children`. It does not change the fact
grammar, add a lifecycle condition site, or change task-extension composition.

Disjunction remains a real language issue: the GitHub observer documentation
identifies its `"NULL"` enum sentinel as a workaround for the inability to
state null or OR in `done_when`. Nothing in this decision needs disjunction,
so `any`, nested groups, and their extension-composition consequences leave
with a future standalone ADR. Keeping composition changes as groundwork would
pay validation and migration cost without behavior used here.

The implementation must add populations to workflow decoding, references,
whole-array cascade, trusted-layer rules, status output, and the resident
supervisor, without adding a new definition kind or provider name to core. It
must accept root `max_up_children` in `config.toml`, canonicalize every
no-real-parent admission under the virtual root, and preserve explicit
sibling-cohort authority independently from that capacity key.

The false defaults intentionally make a bare population declaration
upward-convergent only. It can discover and create desired members, but it does
not fully converge run occupancy or removal until the operator enables the
corresponding actions. The operational cost is visible rather than silent:
destroy decisions remain dry-run events, and idle up members continue consuming
root capacity. The worked examples set both switches to true explicitly because
they show steady-state operation after rollout.

Behavior fixtures and tests cover at least:

- trusted ownership, whole-array replacement, provenance, and collisions;
- poll and push discover validation, complete-snapshot failure, wake coalescing,
  and supervised restart;
- poll destruction from successful absence only, including tombstones and task
  guard deferral, omitted-switch dry-run verdicts, and explicitly enabled
  execution;
- push expiry from the precise external-input clock, including repeated
  appearances, inbound resets, restart persistence, tombstones, and the same
  task guard and automatic-destroy control;
- first-empty status recording, explicit-idle eligibility versus a runtime that
  never reports, default and explicit `auto_down` exclusion, explicitly enabled
  oldest-first selection, ordinary down cleanup, and failed-down fallback;
- the three up signals, canonical resource-state comparison, invalidated idle
  evidence, pending existing-member priority, and concurrent admissions; and
- one virtual-root cap for all parentless ups, including manual commands,
  without granting relation or lifecycle authority between root sessions.

The implementation order is:

1. Add poll and push discover faces to the relevant plugin observers,
   including parameter and item schemas, then add the optional wake stream to
   the poll observer. The Slack adapter exposes its unbound-mention stream
   without deciding a workflow.
2. Complete virtual-root admission and durable status-event lookup, then add
   workflow-population validation and the resident evaluator with provenance,
   snapshot/expiry lifecycle, the built-in task guard, capacity-driven down/up,
   automatic-action permissions, dry-run verdicts, and population events.
3. Cut a downstream deployment over to each user-owned workflow population
   entry and remove its dispatch loop.
4. Remove the Slack opaque command hook and publish its one-time migration.

After cutover, configuration in a downstream deployment retains team
parameters, workflows, task specialization, automatic-action permissions,
expiry, and instructions. Its
deployment infrastructure retains credentials, service management, endpoint
exposure, signature verification, and Terraform. Until the failure-model work
lands, a downstream deployment may also retain the narrow recovery shim that
detects stale produced state and deliberately rebuilds it. That shim does not
enumerate desired resources or choose sessions; this decision neither absorbs
nor legitimizes its verify-before-skip behavior.

The new surface is falsified before implementation is accepted if prototypes
of the three concrete consumers cannot share the discover-item, provenance,
membership, and capacity mechanisms without any of the following:

- provider-specific vocabulary or branching in core;
- dynamic workflow or task selection;
- plugin-owned team repositories, channels, limits, or expiry values;
- a poll member that must be destroyed while it remains in a successful
  complete snapshot;
- a push source that must treat stream termination or a quiet discover stream
  as immediate membership absence, or needs a removal rule that a single
  per-member external-input expiry duration cannot represent;
- a wake stream that must carry trusted discovery items or change membership
  without a complete snapshot;
- automatic destruction of a member with an unsatisfied task, including a task
  whose completion can never become satisfied;
- capacity reclamation from a runtime that has never explicitly reported idle,
  from a manually owned session, or without virtual-root contention;
- automatic down or destroy control that cannot be represented by two
  independent per-entry permissions;
- a safe initial rollout that cannot use upward-only convergence, dry-run
  destruction verdicts, and retained up members until each automatic action is
  explicitly enabled;
- a down member that must come up for a signal other than re-appearance,
  inbound input, or changed observed resource facts;
- a retained population that needs a total bound and deterministic destructive
  eviction rather than snapshot absence or guarded push expiry;
- virtual-root capacity accounting that grants relation, judge, lifecycle, or
  delivery authority between independently created root-level sessions; or
- adoption or destruction of sessions lacking matching workflow-population
  provenance.

If one of those is necessary, the generic population surface is not retained
merely because it was designed here. The incompatible consumer stays plugin- or
deployment-local while a narrower language decision is made. Predicate-based
lifecycle, finer task selection, expiry exceptions, residency guards, and
per-population fairness can each return as pure extensions when a concrete
consumer supplies their semantics. If only one consumer remains after the
poll, push, and orchestrator prototypes, the shared abstraction also loses its
justification under the repository's YAGNI rule.

## Alternatives considered

### Add a top-level population kind

A separate kind gives each population a definition address and can reference
its workflow statically. That identity was attractive while workflows might
arrive as mounted plugin content or be composed by multiple owners. Under the
owner-ratified direction that running workflows are user-owned policy, the
extra kind duplicates that owner and makes the workflow an external endpoint
of policy that exists only to produce sessions of that workflow. The direction
is not yet formally implemented, so this ADR also makes a plugin-layer
population a load error. The required per-entry `name`, whole-array cascade,
trusted-layer restriction, and workflow-plus-entry provenance preserve the
useful identity and safety properties without adding to the kind vocabulary.

### Keep reconcilers in a downstream deployment

Deployment scripts can call `plect up` and `plect destroy` today. They cannot
share core's load-time reference validation, schema roots, provenance guard,
resident supervision, or event history, and every deployment must reproduce
the same convergence and failure rules. This is the observed duplication the
decision removes.

### Make discover a new standalone plugin kind

A standalone `resource_discoverer` could be referenced by both observer and
population. No concrete consumer needs discovery independently of a resource
contract, and it would introduce a second reference whose item resource must
still be checked against an observer. Keeping discover as a face of the
observer makes recognition, observation, finalization, and appearance one
contract for one external resource kind.

### Extend workspace providers instead of resource observers

A workspace provider recognizes a resource for session naming and owns
workspace acquisition. Discovery and changed-resource wake decisions are useful
without a workspace. Putting discovery on the provider would reunite
responsibilities the language deliberately separates and would make a provider
and observer two authorities for the same resource kind.

### Treat push appearances as ordinary session events

There is no session log before the appearance creates a session. Inventing a
placeholder session solely to receive that event would make bootstrap identity
and cleanup circular. The discover stream is ingress to desired-state
evaluation; after creation, ordinary inbound events belong to the real
session's durable log and reset push expiry.

### Configure lifecycle predicates

The first shape considered `destroy_when`, `down_when`, and `up_when`
transition triggers. They state each edge positively and can define a neutral
band, but independently true triggers need precedence and make every population
author commands. Recasting them as a `retain_while` / `up_while` invariant pair
matched `done_when`'s declarative convergence style, but still required every
deployment to encode common lifecycle safety.

The invariant shape then needed an external-input idle leaf, a lifecycle-only
`not` wrapper to express “not idle,” and `grace` for hysteresis. Protecting open
work also required quantification over heterogeneous dynamic task instances.
A `tasks` CEL root with `all` / `exists` comprehensions was expressive but lost
the language's load-time state-key validation inside heterogeneous iterators. A
structural task leaf preserved validation but added task selection and
quantifier vocabulary. Refining either form to ask only whether every task's
existing completion result is satisfied exposed the simpler rule: automatic
population destruction should never discard unsatisfied work at all.

The chosen design therefore replaces configurable destruction predicates with
poll absence or push expiry plus a built-in completion guard, and replaces
up/down predicates with capacity pressure plus explicit runtime-idle and wake
signals. It needs no `not`, idle fact leaf, task quantifier, or grace field. The
trigger triple, invariant pair, and completion-based quantifier remain possible
pure extensions if a concrete consumer later needs policy finer than the safe
built-in mechanisms.

### Add shared `any` groups now

Shallow `any` groups would address an independent wart: an observer documents
its `"NULL"` enum sentinel as a workaround for the fact grammar's lack of null
or OR. Adding them also affects structural schemas, conformance fixtures,
diagnostic traversal, CEL-leaf interaction, and monotone task-extension
composition. Populations no longer need disjunction, so accepting that blast
radius as groundwork would violate YAGNI. A future ADR can decide the grouping
depth and extension rule on their own evidence; this decision leaves the fact
grammar unchanged.

### Add recovery and verify-before-skip

Reconciliation will call the existing idempotent lifecycle paths, so it will
inherit their current produced-state behavior. Adding validity probes,
degraded run state, cleanup-on-failure, or selective reconstruction here would
mix resource-population policy with the independent failure model and obscure
which authority answered whether a session exists versus whether its runtime
is healthy.
