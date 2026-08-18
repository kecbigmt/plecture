# Task nesting

This design is governed by
[`../adr/2026-08-17-task-nesting.md`](../adr/2026-08-17-task-nesting.md).

## Design Core

Inner and outer are homogeneous, so composition is closed. From the outside a
nested task is exactly a task: it presents the surfaces a plain task presents —
an inputs schema, public outputs, `done_when`, `requires`, a budget,
`[health]`, `[[outputs]]`, chains, and `[terminal]` — and nothing that consumes
tasks needs to know whether nesting is inside it. Workflows, chains, downstream
nodes, status, and the orchestrator address a nested task the way they address
any other.

The joint is the only nesting-specific vocabulary: `inner` names the next task
inward, `locals` holds the joint's private intermediates, and the `[bind.*]`
tables wire the boundary. No other nesting-only concept exists.

Task nesting therefore lets an outer task add a layer of its own to an inner
task without copying the inner task's file. Any layer that owns task
definitions writes outer tasks: user-owned configuration, a configuration-only
plugin, and a plugin factoring its own tasks, such as runtime variants that
share a common inner layer. The rules in this design do not vary with the layer
that owns the outer task.

Closure decides the field rules, so they are corollaries rather than a list of
exceptions. Whatever a plain task declares, an outer task declares for its own
layer, and layers compose additively: `done_when` by conjunction, `requires`
and `[done_when.budget]` per layer, `[health]` by AND on `alive` and OR on
`activity`, `[[outputs]]` in the declaring layer's own key namespace, chains on
the composed id. Each layer declares only its own additions and answers only
for them, which is what makes the no-override rule hold by construction.

No task field is unconditionally inner-owned, so closure is realized in full.
What remains are conflict rules rather than ownership rules: `[terminal]`
admits at most one declaration per nesting chain, judge ids and `bind.env` keys
are unique across the chain, and a public output name has one definition source
within its layer.

A nested task has a chain of task definitions. The outermost task is the id
named by a workflow node. Each outer task names its next inner task with
`inner`, and that inner task may itself be nested. The innermost task remains
unaware that it is nested.

The lifecycle is an N-layer LIFO stack:

```text
outermost setup -> ... -> innermost setup -> innermost cleanup -> ... -> outermost cleanup
```

The nested task declares only the public outputs the outer task explicitly
binds or produces. Inner public outputs are not passed through automatically.
The outer task may re-export inner outputs, rename inner outputs, or bind values
computed by its own setup, but downstream consumers read only the outer task's
declared public contract. Outer setup emits locals: always-private intermediate
values available to outer cleanup, input and environment binding templates, and
public output binding. Public output binding is a live projection, not a
setup-time copy: reads render against the current inner output and local values.
Direct inner-output bindings route mutable writes to that inner output;
computed bindings are rendered strings and are read-only.

## Configuration Shape

An outer task is a normal `tasks/<id>.toml` file with an `inner` reference and
optional binding tables:

```toml
inner = "official/claude/claude"

setup = '''
jq -nc --arg guard_dir "{{.Nodes.gh_guard.outputs.dir}}" '{guard_dir:$guard_dir}'
'''
cleanup = "true"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"
socket_path = "{{.Inner.outputs.socket_path}}"
mcp_config = "{{.Inner.outputs.mcp_config}}"
agent_session = "{{.Inner.outputs.session_id}}"
guard_dir = "{{.Locals.guard_dir}}"

[bind.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Locals.guard_dir}}"

[bind.env]
PLECT_TEAM_CONTEXT = "{{.SessionName}}"

[inputs_schema]
type = "object"
required = ["tmux_session"]
additionalProperties = false

[inputs_schema.properties]
tmux_session = { type = "string" }
model        = { type = "string", pattern = "^[A-Za-z0-9_.:-]*$" }
effort       = { type = "string", pattern = "^[A-Za-z0-9_.:-]*$" }

[locals_schema]
type = "object"
required = ["guard_dir"]
additionalProperties = false

[locals_schema.properties]
guard_dir = { type = "string" }

[outputs_schema]
type = "object"
required = ["pid", "socket_path", "mcp_config", "agent_session", "guard_dir"]

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
socket_path = { type = "string", mutable = true }
mcp_config = { type = "string" }
agent_session = { type = "string", mutable = true }
guard_dir = { type = "string" }
```

The outer task's `inputs_schema` is its own schema. It is not an edit to the
inner task's schema. Workflow node inputs validate against the outer schema,
then `bind.inputs` renders the inner input object and validates it against
the inner task's schema.

The `bind.env` table injects environment variables into process executions
owned by the inner task: setup, cleanup, `[health]` probes, dynamic output
scripts, and terminal operation commands. It does not affect the outer task's
hooks, any sibling task, or commands the inner task sends into an interactive
endpoint. Agent runtimes that launch by typing into a terminal
therefore need an author-declared input for launch-line environment exports.

Among the terminal operations, the injection reaches the ones plect runs
itself: `capture`. `send_text` and `send_keys` resolve into another execution
through the `{{terminal "..."}}` helper and carry whatever environment that
execution runs with. `attach` resolves to a command string the caller's own
shell executes, and carries the caller's environment: placing bound values
there would mean synthesizing quoted assignments into a rendered command, and
this design specifies no quoting for that. A runtime needing bound
environment on the attach line declares an author-declared launch input for
it, the same rung-1 answer terminal-launched agent runtimes already take.

Task nesting fields:

| Field | Required | Type | Meaning |
|---|---:|---|---|
| `inner` | yes | string | Task reference for the nested inner task. |
| `setup` | no | string | Outer setup hook. Empty means `{}` locals. |
| `cleanup` | no | string | Outer cleanup hook. Empty marks the outer layer cleaned. |
| `[bind.inputs]` | no | string table | Templates that produce the inner task's inputs. |
| `[bind.env]` | no | string table | Templates that produce the inner task's process environment additions. |
| `[bind.outputs]` | no | string table | Templates that bind the nested task's public outputs from inner outputs or locals. Re-exports and renames are explicit here. |
| `[inputs_schema]` / `inputs_schema_file` | no | JSON Schema | The outer task's workflow-facing inputs contract. |
| `[locals_schema]` / `locals_schema_file` | no | JSON Schema | The private locals contract for outer setup emissions. |
| `[outputs_schema]` / `outputs_schema_file` | no | JSON Schema | The nested task's explicit public output contract. Every public output is declared here. |
| `[done_when]` | no | leaf list plus budget | Completion conditions the outer layer adds to the inner effective `done_when`, with an optional `[done_when.budget]` accounting for those conditions alone. |
| `requires` | no | string list | The public output keys the outer-added `done_when` checks read. |
| `[terminal]` | no | terminal table | An interactive endpoint for a nesting chain whose other layers declare none. |
| `[health]` | no | health table | Liveness and activity probes for the resources this layer brings up. |
| `[[outputs]]` | no | dynamic output entries | Live value sources this layer produces into its own public contract. |
| `[[chains]]` | no | chain entries | Chains attached to the nested task id, with judge ids resolved from the composed effective `done_when` and chain output mappings validated against the outer public output contract. |

The nested task's effective scope is the innermost task's scope. If an outer
task declares `scope`, it must match the scope of its next inner task, and the
rule repeats down the nesting chain.
The inner task's `.Self` sees only the inner task's own outputs. Outer cleanup,
binding templates, and `[bind.outputs]` wiring read outer setup values from
`.Locals`; `[bind.outputs]` wiring also reads inner setup values from
`.Inner.outputs`.
A `[bind.outputs]` entry is a direct inner-output binding only when the whole
template body is exactly one `.Inner.outputs.<key>` reference, apart from
template delimiters and whitespace. Any literal text, function call, pipeline,
multiple template action, or local reference makes the binding computed.
Direct inner-output bindings project the inner output's native value without
string rendering, so integer, boolean, object, and array outputs keep their
schema type. Computed bindings render templates to strings, so their public
schema type is `string`.
Every public key named by `[bind.outputs]` must appear in the effective
`outputs_schema`, and every public field is declared explicitly by the outer
schema. A same-name re-export such as `pid = "{{.Inner.outputs.pid}}"` still
needs a local schema property.

Inspection output such as `plect task show` prints the nesting chain from the
outermost task to the innermost plugin task.

## Layer Outputs

A layer declares `[[outputs]]` for values it needs live rather than frozen at
setup: an observed resource-status key, or a script whose stdout is refreshed
when a check reads it. An outer layer's added `done_when` checks then read
current values on the same standing the inner task's own checks have.

Output keys are layer-scoped. A key an inner layer produces lives in that
layer's namespace and is invisible outside it until `[bind.outputs]` binds it,
so an outer `[[outputs]]` key may carry the same name as an inner-produced key
without colliding. References are always layer-explicit: `.Inner.outputs.<key>`
names the inner layer's key and a public key names the outer contract's. Mutable
writes address the outer public contract and route from there, so output keys
have no flat cross-layer surface on which two same-named keys could meet.

Collisions are banned within one layer's public contract, where one public name
has one definition source. An outer-produced key colliding with a
`[bind.outputs]`-bound key, or with another key the same layer produces, is a
load error.

Outer-produced keys are public outputs, declared in the outer `outputs_schema`
like every other public field, and available to the outer `requires` and
`done_when` under the per-layer scoping those surfaces already follow.

## Completion Conditions

An outer task may declare `[done_when]`. The composed task's effective
`done_when` is the inner effective `done_when` conjoined with the outer's added
leaves. The addition can only narrow completion: `done_when` has one leaf list,
`all`, and no disjunction, negation, reordering, or removal syntax, so every
condition the inner author declared stays necessary and their completion
reasoning holds unchanged. The guarantee covers values as well as leaves: an
output an inner layer's `done_when` check reads is bound as a direct binding of
that inner output. A computed binding or an outer-produced key in its place
would let the outer layer choose what the inner gate sees, neutralizing the
condition without removing it. The composed completion contract is part of the
nested task's public face, which the outer task already owns through its public
output contract.

Each layer may declare `[done_when.budget]`. A budget is patience policy for one
layer rather than a condition, so it sits outside the conjunction. A layer's
budget watches only the conditions that layer declared: a heartbeat tick
consumes it only while that layer's own items are unmet, and exhausting it
escalates naming that layer and that layer's unmet items. A layer that declares
no budget takes the same treatment as a standalone task that declares none;
nesting adds no default of its own, so an inner budget behaves in composition
exactly as it does standalone.

Budgets never interact across layers. There is no arbitration rule, no
outermost-wins selection, and no collision to detect, because two budgets in one
chain account for disjoint condition sets.

Outer `done_when` checks read only the outer public output contract, the same
scope rule outer chains follow. Locals and unbound inner outputs are not visible
to them.

Judge ids from outer leaves join the judge namespace of the inner effective
`done_when`, and a judge id repeated anywhere in the nesting chain is a load
error, so an outer layer cannot mask an inner judge. Verdicts are recorded
against a flat per-instance id namespace — a reviewer's verdict targets an id,
not a layer — so cross-layer uniqueness is what keeps every verdict's target
unambiguous. Budget accounting needs no such sharing, because budget
attribution follows the declaration site. Chain judge ids resolve from the
composed effective `done_when`, so a chain declared on the outer task may name
an inner judge id or one the outer added.

The outer task declares `requires` for its own added checks:

```toml
inner = "official/github/work"

requires = ["review_decision"]

[done_when]
all = [
  { check = "review_decision", eq = "APPROVED" },
  { judge = "the team's release checklist is satisfied", id = "release-gate" },
]

[bind.outputs]
instruction = "{{.Inner.outputs.instruction}}"
review_decision = "{{.Inner.outputs.review_decision}}"
```

The mutual validation between `requires` and `done_when` applies to the outer's
own additions: every outer-added check names a key in the outer `requires`, and
every key the outer `requires` names is a property of the outer
`outputs_schema`. The inner `requires` stays inner-owned and validates the inner
`done_when` against the inner outputs. Each layer validates only its own
additions, and neither layer's `requires` constrains the other.

## Interactive Endpoint

Any one layer of a nesting chain may declare `[terminal]`. An outer task
declaring it over an inner chain that declares none composes an interactive
surface around a headless inner task, which is addition rather than
modification. Two layers of one chain declaring `[terminal]` is a load error
naming both layers.

The per-chain rule is one this design introduces, and it is what keeps plan
assembly's existing count sound. Plan assembly rejects a plan whose nodes
declare more than one `[terminal]`, counting nodes; a nesting chain sits inside
one node, so without a per-chain rule two layers could place two declarations
behind a single node and pass that count. With it, a nested task contributes one
terminal declaration or none, and terminal-task resolution for attach, capture,
and the `{{terminal "..."}}` helper needs no nesting-aware tie-break.

Whichever layer declares `[terminal]`, the composed public contract carries
`interactive_endpoint` bound from that layer. When the inner chain declares it,
`[bind.outputs]` binds `interactive_endpoint` from
`{{.Inner.outputs.interactive_endpoint}}`. When the outer task declares it, the
outer binds `interactive_endpoint` from its own setup-emitted local. A chain
with a `[terminal]` declaration whose composed public contract does not bind
`interactive_endpoint` is a load error either way.

Terminal operation commands belong to the declaring layer's `[terminal]` table.
Other layers do not inject, wrap, or replace them, and consumers reach the
declaring layer only through the composed public contract.

## Layer Health

`setup`, `cleanup`, and `[health]` are one lifecycle trio, and each layer of a
nesting chain owns its own. An outer task declares `[health]` for the resources
that layer brings up.

Composition across the layers of a chain has the same dual shape as composition
across instances. `alive` is the AND: any failing layer makes the composed task
unhealthy, and the report names the failing layer. `activity` is the OR:
evidence from any layer counts. A layer declaring no `[health]` contributes
nothing to either composition — vacuous in the AND, no vote in the OR — and
neither does one whose activity probe emits no envelope. Each layer declares
only its own probes and never replaces another layer's, so composition needs no
conflict rule.

A layer earns an activity probe only when its activity indicates session
progress. A chatty sidecar layer with an activity probe would mask a stalled
agent.

Probe semantics and the activity envelope are specified by
[`health-declaration.md`](health-declaration.md).

## Reference Resolution

`inner` accepts a normal task id or a catalog-qualified task reference:

```toml
inner = "claude"
inner = "official/claude/claude"
```

The normal task id uses the merged task namespace and therefore follows the
usual layer rules. The catalog-qualified form selects a task inside an enabled
plugin without consulting same-id user shadows. It parses the same way as
qualified `{{bin}}` references: plect tries every mounted plugin reading and
accepts exactly one candidate. Zero candidates or multiple candidates are load
errors, so arbitrary-depth plugin paths never rely on a longest-prefix guess.

Workflow node `uses` accepts the same catalog-qualified form:

```toml
[[nodes]]
id = "runtime"
uses = "official/claude/claude"
```

A qualified workflow `uses` lets a user-owned workflow opt out of a same-id
shadow deliberately. Shipped plugin workflows continue to use task ids because
catalog aliases are user-local.

A plugin's own outer task names its inner task with the relative form, which
resolves in the same-plugin namespace. Cross-plugin nesting is excluded by the
reference grammar rather than by a validation rule: naming another plugin's
task requires the catalog-qualified form, and catalog aliases are user-local,
so plugin-authored config has no vocabulary for writing one.
Catalog-qualified plugin identity, and the user-chosen local alias it starts
with, are specified by [`plugin-packaging.md`](plugin-packaging.md).

## Validation Rules

Loading nested task definitions fails when:

- `inner` names an unknown task, an unknown catalog alias, a disabled plugin, or
  a task missing from the selected plugin.
- `inner` forms a nesting cycle, including self-reference.
- the outer task declares a `scope` that differs from the inner task's scope.
- an outer `[[outputs]]`-produced key collides with a `[bind.outputs]`-bound key
  or with another key the same layer produces.
- an outer `[[outputs]]`-produced key is missing from the outer
  `outputs_schema`.
- two layers of the nesting chain declare `[terminal]`.
- an outer `done_when` judge id repeats a judge id declared by any other layer
  in the nesting chain.
- an outer `done_when` check names a key missing from the outer `requires`, or
  the outer `requires` names a key missing from the outer `outputs_schema`.
- `[bind.outputs]` references a source other than an inner public output or a
  local.
- `[bind.outputs]` declares a public key missing from `outputs_schema`.
- `[bind.outputs]` declares a computed template output mutable in
  `outputs_schema`.
- `[bind.outputs]` declares a computed template output with a non-string
  `outputs_schema` type.
- `[bind.outputs]` declares a direct inner-output binding whose
  `outputs_schema` type differs from the bound inner output's type.
- `[bind.outputs]` declares a direct inner-output binding mutable in
  `outputs_schema` when the bound inner output is not mutable.
- `bind.inputs` omits an inner required input or binds a key rejected by a
  closed inner schema.
- a `bind.env` key is not a valid process environment name.
- a `bind.env` key repeats a key from any other layer in the nesting chain.
- a chain declared on the outer task references a judge id not declared by the
  composed effective `done_when`, or a public output not declared by the outer
  public contract.
- a chain declared on the outer task references a local not bound into the outer
  public contract.
- a layer of the nesting chain declares `[terminal]` and the outer public
  contract does not bind `interactive_endpoint` from that layer.
- a `done_when` check at any layer of the composed effective `done_when`, or a
  chain, references an output not bound by the outer public contract.
- an output read by an inner layer's `done_when` check is bound by anything
  other than a direct binding of that inner output, including a computed
  binding or a key the outer layer produces.

Running a nested task fails when:

- the outer setup exits non-zero or emits locals rejected by the locals schema;
- a binding template fails to render;
- rendered bound inputs fail the inner task's schema value validation;
- a public output binding template fails to render or emits a value rejected by
  the public outputs schema;
- any inner task process fails under the normal inner task rules.

Cleanup skips layers that never reached setup. If an inner setup fails after an
outer setup succeeds, the next cleanup unwinds each produced outer layer.

## Customization Ladder

Task nesting is the middle rung:

1. Parameterization: a plugin task author declares a supported input.
2. Task nesting: an outer task adds lifecycle, locals, env, chains, completion
   conditions, an interactive endpoint, input binding, and explicit output
   binding.
3. Fork: a user copies the whole task because the required behavior is not an
   author-declared variation point and cannot be added outside the task.

Workflow node inputs remain the home for user default values. Script-internal
variation remains parameterization work for the plugin task author.
Author-declared parameters are data-shaped: values, paths, naming templates,
booleans, and environment maps. They are never behavior-shaped script hooks,
executable substitution, or output-shape hooks; a behavior-shaped parameter
hides a shadow behind a keyhole and breaks final-by-default ownership.

## Plugin-Counterpart Shadow Mapping

The evaluation set contains seven production shadows with plugin counterparts.
Runtime-argument wiring for a retiring third-party service is outside this
mapping. Explicit outer-output binding applies to task definitions; workspace,
resource, and channel shadows resolve through plugin parameterization or
adoption in their owning config kinds.

| Shadow | Durable intent | Rung | Zero-copy path |
|---|---|---:|---|
| `tasks/claude.toml` | Different runtime inputs, extra agent-process env, path injection, chains. | 1 + 2 | Rung 2 binds `tmux_session`/model/effort/path inputs and declares chains on the outer id. Rung 1 in the `claude` plugin provides an author-declared `launch_env` input whose exports are included in the terminal launch line. Workflow-owned defaults stay in the workflow node. |
| `tasks/codex.toml` | Different runtime inputs, extra agent-process env, path injection, chains. | 1 + 2 | Rung 2 binds the plugin task inputs and declares chains. Rung 1 in the `codex` plugin provides an author-declared `launch_env` input whose exports are included in the Codex TUI launch line. |
| `tasks/codex_exec.toml` | Different runtime inputs, extra worker-process env, path injection, worker state location. | 1 + 2 | Rung 2 binds runtime/path inputs and declares chains. Rung 1 in the `codex` plugin provides author-declared `launch_env` and `state_root` inputs for the worker's exported environment and state directory. |
| `tasks/work.toml` | Different instruction input, explicit output boundary, team-specific completion gates, and local chain attachment. | 2 | The outer task binds `instruction` into `official/github/work`, re-exports chosen inner outputs through `[bind.outputs]`, binds any local computed public output through `[bind.outputs]`, adds team-specific `done_when` leaves with a matching `requires`, and attaches chains to the outer id. Workflow-owned defaults stay in the workflow node. |
| `channels/codex_exec.toml` | Queue timeout and message formatting around the shipped enqueue executable. | 1 | The channel adopts the plugin's shipped enqueue executable. Rung 1 in the `codex` plugin declares `enqueue_timeout` and `message_envelope`; `message_envelope` is a formatting template over the existing event fields, not executable substitution. |
| `workspaces/github.toml` | Workspace layout root, branch naming, cleanup policy, and local-only status outputs. | 1 | Rung 1 in the `github` plugin declares `workspace_layout_root`, `issue_branch_template`, `tagged_branch_suffix`, and `delete_branch_default`. The plugin already owns `title`; the local-only `checks_status` output is dropped from workspace setup because resource observation owns CI status. |
| `resources/github.toml` | Script-internal observation drift with the same public state keys, plus review-state observation needed outside workspace setup. | As-is + 1 | Adopt the plugin resource implementation as-is for the existing state keys. Rung 1 in the `github` plugin adds the concrete `review_decision` observed state key; no open-ended resource output hook is needed. |

The mapping leaves no listed plugin-counterpart shadow on rung 3. Configs with
no plugin counterpart, such as local envfile, chat thread binding, initial task,
agent docs, orchestrator workspace, and local templates, are user-owned config
and are outside the shadow count.

## Worked Example

A team-local Claude runtime customization can stay small:

```toml
inner = "official/claude/claude"
setup = "jq -nc --arg guard_dir '{{.Nodes.gh_guard.outputs.dir}}' '{guard_dir:$guard_dir}'"

[bind.outputs]
pid = "{{.Inner.outputs.pid}}"
socket_path = "{{.Inner.outputs.socket_path}}"
mcp_config = "{{.Inner.outputs.mcp_config}}"
agent_session = "{{.Inner.outputs.session_id}}"
guard_dir = "{{.Locals.guard_dir}}"

[bind.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Locals.guard_dir}}"

[bind.env]
PLECT_TEAM_CONTEXT = "{{.SessionName}}"

[inputs_schema]
type = "object"
required = ["tmux_session"]
additionalProperties = false

[inputs_schema.properties]
tmux_session = { type = "string" }
model = { type = "string", pattern = "^[A-Za-z0-9_.:-]*$" }
effort = { type = "string", pattern = "^[A-Za-z0-9_.:-]*$" }

[locals_schema]
type = "object"
required = ["guard_dir"]
additionalProperties = false

[locals_schema.properties]
guard_dir = { type = "string" }

[outputs_schema]
type = "object"
required = ["pid", "socket_path", "mcp_config", "agent_session", "guard_dir"]

[outputs_schema.properties]
pid = { type = "integer", mutable = true }
socket_path = { type = "string", mutable = true }
mcp_config = { type = "string" }
agent_session = { type = "string", mutable = true }
guard_dir = { type = "string" }
```

The same pattern covers task-shaped shadows whose differences are input values,
chain declarations, added completion conditions, explicit output binding, or a
single command path expressed as an environment/path input. Agent-process
environment for terminal-launched runtimes belongs to author-declared launch
inputs on the runtime plugin. Workspace layout belongs to the workspace
provider or workflow node inputs. Script-internal command changes belong to
parameterization; if no author-declared variation point exists, a fork remains
the explicit last resort.
