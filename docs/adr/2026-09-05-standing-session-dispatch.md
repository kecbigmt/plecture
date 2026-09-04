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
- Conditions use the existing check and CEL expression leaves. Plecture does
  not add a predicate language of its own
  ([tasks](../language/tasks.md),
  [language overview](../language/README.md)).

The missing construct is deployment policy. A plugin can say how resources
are discovered, but it cannot choose a team's repositories, labels, chat
channels, workflow, task, concurrency, or retention policy. The owner-ratified
[exemplar-workflow direction](https://github.com/kecbigmt/plecture/issues/155)
makes running workflows user-owned: catalogs ship copy-templates, never
mounted workflow definitions. Its premise that workflows are policy changes
where standing population policy belongs.

This decision concerns desired session presence. It does not change whether a
persisted node is still valid, whether `plect up` verifies before skipping it,
or how a partially failed run is represented. Those recovery questions remain
the responsibility of [the failure-model work](https://github.com/kecbigmt/plecture/issues/371).

## Decision

### 1. Placement: nest populations in the user-owned workflow

#### Options

| Option | Load-time validation | Layer and cascade owner | Plugin-boundary consequence | Assessment |
|---|---|---|---|---|
| (a) New top-level kind | The observer, optional initial task, target workflow, both input contracts, predicate roots, and item roots resolve from one definition. | A trusted layer owns a separate whole definition and a static workflow reference. | Plugin mechanics and deployment values remain separated, but population policy gets an identity outside the user policy it activates. | Viable, but unnecessary once a running workflow is always user-owned; it adds a kind and a second policy owner for no additional validation. |
| (b) Array nested in `workflow` | The target workflow and its input contract are local; observer, task, discover, item, and predicate references still resolve statically. | The user-owned workflow owns its complete population array. A deeper eligible workflow layer replaces that array wholesale, in the same cascade family as `[tick]` rather than additive `nodes`. | Catalogs may show commented scaffold placeholders, but mounted plugin content cannot activate them. Team parameters remain in the user's copy. | Recommended: a standing population is policy for producing sessions of this workflow, which is exactly the responsibility the user-owned workflow already carries. |
| (c) Nested in `task` beside chains | The observer facts are local and a target workflow can be resolved like a chain. | Task `extends` is additive, so population policy would compose through task specialization even though only one deployment should own a population. | Shipped tasks would either contain team data or require a user to replace or extend plugin work merely to choose deployment policy. | Rejected: chains fire from an existing instance, while discovery fires before an instance exists and must also support sessions that intentionally start without a task. |
| (d) Added to `config.toml` | References and schemas could be checked, but the entry would have no definition identity of its own. | The reserved file is machine-wide resolution and defaults, outside definition discovery and cascade. | It would centralize every deployment rule in a reserved file and provide no address for provenance or events. | Rejected: it violates the definition-block principle and cannot name the authority allowed to destroy a session. |
| (e) Nested in `resource_observer` | Discovery, item schema, and lifecycle fact roots would be colocated. The workflow and task could also be resolved. | Resource observers are whole-definition plugin resources; a user cannot append policy to one and replacing one copies plugin mechanics into the deployment layer. | Team repositories, channels, limits, and workflow choices would land in plugin-owned files or force a fork. | Rejected: the observer should gain the reusable discovery mechanism, not deployment policy that consumes it. |

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

### 2. Population block name

#### Options

The collection field chosen here and each entry's required `name` are
distinct. This decision names the array below the workflow; the entry name is
the stable block identity used with the workflow id for provenance.

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

#### Recommendation

Recommend `populations`, pending an explicit owner ruling. Each
`[[<workflow>.populations]]` entry declares one desired population, and its
required `name` identifies that entry for provenance. The plural spelling
follows the language's array-field convention, while “population” covers both
the discover/admit face and the retain/remove face without naming evaluator
mechanics. The examples below use this recommendation so the proposed shape
is concrete; choosing another candidate changes the collection path and event
namespace, not the semantics in Decisions 3–7.

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
single authority for resource recognition, discovery, and `destroy_when`
facts. The loader resolves it and requires it to declare `discover`. When the
entry also declares an initial `session.task`, the loader requires that task's
observer to resolve to the exact same definition. The two references answer
different questions—what population is discovered and what work starts—but
their load-time equality prevents them from becoming competing resource
authorities.

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
required = ["resource_kind", "checks_status", "revision", "pr_url", "mergeable_state", "review_decision", "review_reply_state", "lifecycle_state"]

[pull.state_schema.properties]
resource_kind   = { type = "string", enum = ["pull"] }
checks_status   = { type = "string", enum = ["SUCCESS", "PENDING", "FAILURE", "NULL"] }
revision        = { type = "string" }
pr_url          = { type = "string" }
mergeable_state = { type = "string", enum = ["clean", "dirty", "unstable", "blocked", "behind", "unknown", "draft", "has_hooks"] }
review_decision = { type = "string", enum = ["APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", "NULL"] }
review_reply_state = { type = "string", enum = ["waiting_for_human", "human_replied", "not_applicable"] }
lifecycle_state = { type = "string", enum = ["open", "closed", "merged"] }
```

The wake executable is introduced with this discover face. It connects to a
webhook-receiver service and streams hints; it does not emit pull-request
records. Exposing the webhook endpoint and verifying request signatures are
deployment-infrastructure responsibilities outside the configuration
language. The current pull observer's aggregate `review_decision` cannot say
whose reply is next. The sketch therefore also introduces
`review_reply_state`, derived by the plugin from the authenticated review
actor and review-thread participants: `waiting_for_human` means that actor's
latest relevant message awaits a human response, `human_replied` means a
later human response needs handling, and `not_applicable` means neither state
has been established. Core sees only the declared fact contract.

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

### 4. Session and lifecycle surface

#### Options

1. Let the plugin discover action return complete `plect up` commands. This
   puts workflow and team policy in executable output and makes topology
   dynamic.
2. Give each population its own predicate syntax and timeout expressions. This
   creates a second language for facts already expressible by task conditions
   and moves duration arithmetic into CEL.
3. Make session topology static, session values schema-checked, and lifecycle
   conditions reuse existing fact leaves.

#### Recommendation

Use this closed workflow-population surface:

| Field | Requirement | Meaning |
|---|---|---|
| `populations.name` | Required identifier, unique within the workflow. | Gives this entry stable provenance below the workflow address. |
| `populations.resource_observer` | Required static resource-observer reference. | Selects the sole recognition, discovery, and population-resource lifecycle authority. |
| `populations.discover_inputs` | Required table. | Literal deployment data validated against the observer's discover `inputs_schema`. |
| `populations.session.task` | Optional static task reference. | Selects a task to set up after the session is up; its observer must equal `resource_observer`. |
| `populations.session.inputs` | Optional value table. | Session inputs over literals, `resource.id`, and the discover `item_schema`'s `discovery.*` properties. |
| `populations.session.destroy.force` | Optional boolean; default false. | Uses the lifecycle service's explicit force-destroy path for entry-owned sessions. |
| `populations.poll_every` | Required positive duration for poll; forbidden for push. | Sets complete-snapshot cadence. |
| `populations.idle` | Required positive duration for push; forbidden for poll. | Makes a push-owned session eligible for expiry after no inbound activity. |
| `populations.grace` | Optional non-negative duration for push; forbidden for poll; default zero. | Requires idle eligibility to remain continuous before destruction. |
| `populations.destroy_when` | Optional conjunction of check or expression leaves. | Destroys an owned session when current population-resource facts satisfy it. |
| `populations.down_when` | Optional conjunction of check or expression leaves; requires `up_when`. | Takes an owned up session down when current population-resource facts satisfy it. |
| `populations.up_when` | Optional conjunction of check or expression leaves; requires `down_when`. | Brings an owned down session up when current population-resource facts satisfy it. |
| `populations.keep_while.task` | Required static task reference when `keep_while` is present; push only. | Selects dynamic task instances whose own resource facts can retain the session. |
| `populations.keep_while.all` | Required conjunction of check or expression leaves when `keep_while` is present; push only. | Suppresses idle expiry while any selected task instance satisfies it. |

The complete `session.inputs` object is validated against the target
workflow's `inputs_schema` at load time: literal types are checked directly,
`resource.id` is a string, and every `discovery.*` path is resolved against
the observer's discover `item_schema`. Runtime validation still checks schema
constraints that static projection cannot prove. When `session.task` is
present, every task input not supplied explicitly is bound from the session
inputs by the existing dynamic task-setup rules, and each common field must
also satisfy the task's `inputs_schema`.

`resource_observer`, `session.task`, and `keep_while.task` are static topology.
They accept the ordinary relative or catalog-qualified reference grammar and
cannot be CEL expressions. The containing workflow's workspace provider and
the population observer must both match each discovered `resource`; the
provider remains the single authority for session naming.

`destroy_when`, `down_when`, `up_when`, and `keep_while.all` use the check and
expression leaf forms from `done_when` and chain `when`; none accepts judge
leaves. The first three expose only `resource.state.*`, resolved against the
population observer's `state_schema`. Each selected `keep_while.task`
instance exposes `resource.state.*` from its own observer and `self.state.*`
from its task state, exactly as that task's `done_when` does. Its keys resolve
at load time against those two schemas. No matching task instance means false;
multiple matching instances use any semantics. This lets an explicitly
escalated conversation remain alive because of a later task bound to another resource,
without teaching either plugin about the other's resource type or adding a
cross-resource join to core. Every lifecycle decision observes each relevant
resource once, and all leaves for that resource read the coherent snapshot.
An observation or expression failure is fail-closed and changes no lifecycle
state.

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
remains an independent immediate population-resource-fact path.

Capacity is not declared on a population entry. Decision 7 completes the
session tree with a virtual root so the existing `max_up_children` vocabulary
governs both real-parent and parentless admission.

A valid push appearance is persisted in population-evaluator state before
admission. If the virtual-root cap is full, it remains pending and is admitted
in resource-id order when capacity becomes available; process restart does
not forget an appearance core already accepted. A repeated appearance
replaces the pending production record for that resource. Its idle clock
begins only when the session is created, so waiting behind the cap cannot
cause silent expiry.
Successful destruction forgets that appearance, and a later appearance may
create a new session. A repeated push appearance also guarantees that an
owned down session becomes an up candidate through the ordinary `plect up`
path. If the virtual-root cap is full, the persisted appearance keeps that
re-up pending and gives it existing-member priority when a slot opens; it is
never discarded as a duplicate no-op.

Creation, up, down, and destruction use the same service paths as `plect up`,
`plect down`, and `plect destroy`, including resource allowlists,
workspace-provider resolution, cleanup, and errors. `session.destroy.force`
is an explicit choice to pass the same force option; discovery itself never
silently weakens cleanup guards. A population entry persists its containing
workflow address and entry name on sessions it successfully creates and may
destroy only those sessions. It never
adopts an existing session with the same derived name. A population/chain or
population/population name collision emits a conflict and leaves the existing
session untouched.

When `session.task` is present, a successful `up` is followed by the existing
task-setup service with the fixed instance name `initial` and the discovered
`resource`. The evaluator first reads session state: an `initial` instance
with the exact resolved task and resource is already converged; a missing one
is set up; and any conflicting `initial` instance fails closed. The population
does not use `plect up --task`, because that flag is only workflow-input
shorthand and does not instantiate a task by itself. A population with no
`session.task` starts only its containing workflow. It may receive dynamically
created tasks later through the ordinary task-setup path.

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

[review_agent.populations.destroy_when]
all = [{ check = "resource.state.lifecycle_state", in = ["closed", "merged"] }]

[review_agent.populations.down_when]
all = [{ check = "resource.state.review_reply_state", in = ["waiting_for_human"] }]

[review_agent.populations.up_when]
all = [{ check = "resource.state.review_reply_state", in = ["human_replied"] }]
```

Every reference in the example is static. `official.github.review` exists as
a task written for `official.github.pull`; the ADR adds that observer's
discover and `lifecycle_state` contracts. The workflow, population, credentials,
team parameters, and instructions are user-owned. Each `discovery.*` path is
declared by the item schema, `resource.state.lifecycle_state` resolves through
the population observer, and every workflow root and plugin definition already
exists. The initial task receives its declared `app_id`, `owner`, `repo`,
`private_key_path`, and `instruction` inputs from the session after `up`. The
current observer cannot infer a reply turn from `review_decision`, so the
example deliberately relies on the newly declared `review_reply_state`: it
takes a session down only while a human response is outstanding and brings it
up after that response arrives. Each successful down frees one virtual-root
slot for a newly discovered pull request; the reply-driven up waits for a slot
if all root capacity is active again.

#### Operations-chat configuration

This complete user-owned TOML binds the Slack push face above to an operations
workflow. The triggering `mention_ts` is a typed discovery-item field, and
later inbound thread events extend the idle deadline without a dispatcher
process. The session intentionally starts without a task.

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
idle              = "8h"
grace             = "15m"

[ops_chat_session.populations.discover_inputs]
base_url    = "http://127.0.0.1:7890"
channel_ids = ["C01234567"]

[ops_chat_session.populations.session.inputs]
slack_base_url = "http://127.0.0.1:7890"
mention_ts     = { from = "discovery.mention_ts" }

[ops_chat_session.populations.keep_while]
task = "official.github.investigate"
all  = [{ check = "resource.state.issue_status", in = ["PENDING"] }]
```

The Slack plugin sketch introduces `official.slack.thread_state` and its
`discovery.mention_ts` contract. All other references and roots are existing
workflow, workspace, node-output, session-input, task, effect, and channel
surfaces. An explicit escalation may later create an
`official.github.investigate` instance, bound to an issue resource, through
the ordinary dynamic task-setup path. An open issue observes
`resource.state.issue_status = "PENDING"`, so that instance retains the chat
session without adding issue knowledge to the Slack plugin. With no such
instance, idle expiry proceeds.

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

Each evaluation is plan-then-apply. It computes the complete set of allowed
creates, idempotent ups, downs, initial task setups, and owned destroys before
mutating state, then invokes the existing lifecycle and task-setup services.
For a new member it runs `up` before task setup; for a removed member it never
sets up a task before destruction. Concurrent evaluations of one population
are coalesced. A failed mutation remains visible and is retried on the next poll,
appearance, inbound event, or expiry deadline; it does not stop or roll back a
different session whose lifecycle already completed.

Population-created sessions then use ordinary workflow tick reactors. The
population evaluator does not tick tasks, interpret completion, or deliver
notifications. It records `plect.workflow_population.*` decision, conflict,
and failure events on an affected session. Workflow `[[event.channel]]`
bindings may relay those events like any other. A discover failure with no
owned session is visible in resident logs; the language gains no destination,
message, or notification field to special-case it.

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

### 7. Down/up policy for population sessions

#### Options

1. Destroy a session while it waits and recreate it when work returns. This
   releases resources but discards the state, workspace, event log, and thread
   continuity that make it the same session.
2. Declare only `down_when` and bring the session up whenever its logical
   inverse holds. This is compact, but the check-leaf grammar has no general
   inverse and a boundary with no neutral region can cycle on noisy facts.
3. Pair `down_when` with `up_when`. Both use existing fact leaves and existing
   run-state operations, while distinct conditions can encode a neutral band.
4. Bring every down session up on any inbound event. This is responsive, but
   lets unrelated channel traffic override the workflow's resource policy.

#### Recommendation

Allow `down_when` and `up_when` only as an optional pair on a population
entry. They use the same conjunction of check or CEL expression leaves as
`destroy_when`, expose only `resource.state.*`, resolve every key against the
population observer's `state_schema` at load time, and reject judge leaves.
Requiring both avoids inventing negation for check leaves and lets the owner
choose disjoint conditions with a neutral region. No new run state or
lifecycle operation is introduced: sessions go down and come up with the
semantics already defined by `plect down` and `plect up`.

The evaluator observes the resource once and evaluates `destroy_when`,
`down_when`, and `up_when` against that coherent snapshot. Precedence is
`destroy_when`, then `down_when`, then `up_when`. An up session satisfying
`down_when` goes down through the ordinary service path. A down session stays
down until `up_when` holds, except for the push re-appearance guarantee in
Decision 4. Overlapping down/up conditions therefore settle down rather than
cycling within one evaluation. An observation or expression failure causes no
transition. A down or up service error leaves the ordinary lifecycle path's
persisted result authoritative, is not treated as success, records a failure
event, and is retried later.

Successful evaluator-initiated transitions record
`plect.workflow_population.down` and `plect.workflow_population.up` on the
affected session, using the existing population event namespace rather than a
new lifecycle noun.

A successful poll snapshot, including one requested by a wake hint, observes
every present member and may bring a down member up when `up_when` holds. A
push re-appearance of an owned down member always requests its re-up and does
not require `up_when` to be true. An inbound event continues to append to a
down session's durable log and triggers an immediate population lifecycle
evaluation. It does not itself override policy: for poll, and for push events
that are not discover re-appearances, the fresh observation must satisfy
`up_when` before the evaluator brings the session up. Scheduled lifecycle
deadlines use the same evaluation path. Concurrent triggers still coalesce
per population.

The session forest is completed as one tree with a non-addressable virtual
root. Every session with no real session parent is logically its direct child,
whether created by a population, dispatched manually, or placed beside a
parentless session through the existing `root:<session>` marker. The virtual
root has no workflow, workspace, resource, event log, or lifecycle operation;
it is the structural parent needed to apply the ordinary child-cap rule at
the top of the tree. Parentless sessions may continue to store an empty
`ParentSession`; the virtual edge is derived rather than persisted.

The virtual root must not turn independent root-level sessions into trusted
reviewers or lifecycle actors for one another. The existing `root:<session>`
marker continues to record an explicit sibling cohort for relation, judge,
and terminal-delivery semantics, while resolving to the virtual root for
capacity accounting. Two independently created root-level sessions therefore
remain unrelated for authority even though both consume the virtual root's
direct-child capacity. This separation is required because the current
session-scoped implicit roots deliberately prevent unrelated root sessions
from gaining sibling authority.

#### Capacity options

1. Apply the virtual-root cap only to evaluator-driven up operations. Manual
   dispatch would retain its current freedom, but could silently bypass the
   same host constraint the evaluator is required to respect.
2. Apply it to every up of a logical virtual-root child, including manual
   `plect up`. This changes manual behavior when an operator has explicitly
   configured a cap, but leaves one admission authority and one counting rule.
3. Also add per-population caps for allocation fairness. This could reserve or
   divide root capacity, but no concrete contention between populations yet
   defines the intended allocation policy.

#### Capacity recommendation

Use option 2. The virtual root's optional positive `max_up_children` is
declared in `config.toml`, because machine-wide resolution and defaults are
the only existing owner for a root that has no workflow. Unset retains
unlimited parentless ups. A real parent's cap remains on that parent's
workflow and is unchanged. Each session counts only against its logical
immediate parent's cap, so real children are not also counted at the virtual
root.

The documented counting rule is unchanged: a child counts while its run state
is up, an in-flight up admission also counts, and an idempotent up of an
already-up child is exempt. `--force-recreate` still holds an admission because
it first tears run state down. A successful down immediately frees a slot.
Reducing a cap below its current up count selects no victims; new ups wait or
fail until the count is within the bound.

Manual `plect up` at the configured virtual-root cap returns the same cap
error as an up under a capped real parent. This is the intended meaning of an
operator declaring a host boundary: silently exempting the manual path would
make the bound advisory. The operator may take another root session down,
raise the machine setting, and retry. Population evaluators instead persist
their desired create or re-up, defer it without treating the cap as a
lifecycle failure, and retry on later cycles, wake hints, inbound events, or
capacity-changing transitions.

Within one population evaluation, eligible owned down members take available
slots before never-created members, and each class is ordered by resource id;
this prevents a reply to an existing session from being starved by newly
discovered work. Atomic admissions preserve the shared root cap when
different populations or manual commands race, but no cross-population
fairness or reservation is promised. A per-population fairness limit can be
added later without changing virtual-root counting if observed contention
defines how it should allocate capacity.

There is no separate cap on total retained membership. Such a cap would
either reproduce saturation with down sessions or require an eviction policy
that destroys continuity, without a concrete consumer defining which member
may be discarded. Poll snapshot absence, `destroy_when`, and push
idle/`grace` remain the ways retained membership ends. A demonstrated need to
bound retained down state with deterministic eviction would require a
separate decision rather than overloading `max_up_children` with a second
question.

Going down runs ordinary run-scoped cleanup while preserving the session,
workspace, event log, session-scoped state, and population provenance. The
Slack subscription cleanup tombstones its delivery watermark, so the next up
restores it and does not redeliver the transcript. Going up idempotently
recreates run-scoped nodes such as the agent, pane, and credential guard and
resumes preserved dynamic task state. Whether produced state that claims to
be live is actually healthy remains the independent failure-model question in
[issue #371](https://github.com/kecbigmt/plecture/issues/371).

The paired predicates are the initial flapping guard: owners can make the
down and up boundaries disjoint, and unchanged facts cause no repeated
operation. This decision adds no timer beyond the existing push idle
`grace`. If a concrete consumer cannot express a stable neutral band and
demonstrates repeated transitions, a duration-based guard should be designed
from that evidence rather than preemptively adding another clock.

## Consequences

Implementation changes the configuration language and therefore requires a
dialect increment, a migration procedure with a backup step, structural schema
updates, and conformance fixtures for every valid, invalid, and boundary case.
The implementation must add the chosen workflow population array to workflow
decoding, references, whole-array cascade, trusted-layer rules, status output,
and the resident supervisor, without adding a new definition kind or any
provider name to core. It must also accept root `max_up_children` in
`config.toml`, canonicalize every no-real-parent admission under the virtual
root, and preserve explicit sibling-cohort authority independently from that
capacity key.

The implementation order is:

1. Add poll and push discover faces to the relevant plugin observers,
   including parameter and item schemas, then add the optional wake stream to
   the poll observer. The Slack adapter exposes its unbound-mention stream
   without deciding a workflow.
2. Complete the virtual-root admission path, then add language validation and
   the resident evaluator, including provenance, fail-closed planning,
   admission, down/up policy, expiry, and ordinary session events.
3. Cut a downstream deployment over to each user-owned workflow population
   entry and remove its dispatch loop.
4. Remove the Slack opaque command hook and publish its one-time migration.

After cutover, configuration in a downstream deployment retains team
parameters, workflows, task specialization, and instructions. Its deployment
infrastructure retains credentials, service management, and Terraform. Until
the failure-model work lands, a downstream deployment may also retain the
narrow recovery shim that detects stale produced state and deliberately
rebuilds it. That shim does not enumerate desired resources or choose
sessions; this decision neither absorbs nor legitimizes its
verify-before-skip behavior.

The new surface is falsified before implementation is accepted if prototypes
of the three concrete consumers cannot share the same discover-item, provenance,
and lifecycle contracts without any of the following:

- provider-specific vocabulary or branching in core;
- dynamic workflow or task selection;
- a cross-resource join or a new predicate language;
- plugin-owned team repositories, channels, limits, or retention values;
- a push mode that must infer absence from stream silence;
- a wake stream that must carry trusted discovery items or change membership
  without a complete snapshot;
- a down/up policy that needs facts outside its resource observer or a new run
  state;
- a retained population that needs a total bound and deterministic eviction
  rather than explicit absence, destruction, or push expiry;
- virtual-root capacity accounting that grants relation, judge, lifecycle, or
  delivery authority between independently created root-level sessions;
- adoption or destruction of sessions lacking matching workflow-population
  provenance.

If one of those is necessary, the generic population surface is not retained
merely because it was designed here. The incompatible consumer stays plugin- or
deployment-local while a narrower language decision is made. If only one
consumer remains after the poll, push, and orchestrator prototypes, the shared
abstraction also loses its justification under the repository's YAGNI rule.

## Alternatives considered

### Add a top-level population kind

A separate kind gives each population a definition address and can reference
its workflow statically. That identity was attractive while workflows might
arrive as mounted plugin content or be composed by multiple owners. Once
running workflows are always user-owned policy, the extra kind duplicates
that owner and makes the workflow an external endpoint of policy that exists
only to produce sessions of that workflow. The required per-entry `name`,
whole-array cascade, trusted-layer restriction, and workflow-plus-entry
provenance preserve the useful identity and safety properties without adding
to the kind vocabulary.

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
