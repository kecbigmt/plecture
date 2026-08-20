# Tasks

A task definition describes one unit of lifecycle: what to bring up, how to
observe it, what its public outputs are, and — for an instance — when it is
done.

## Two instantiation modes

The kind is one and the contract is one, but a task is instantiated two ways.

| Mode | Reached by | Identity |
|---|---|---|
| Node | A workflow's `[[nodes]]` | Position in the workflow DAG |
| Instance | `plect task setup` | The resource plus the instance name |

Every field declares which modes it is valid in. The structural schema records
this per field as `x-plect-modes`; Plecture semantic validation reports a field
used outside its modes as `PLECT-CFG-FIELD-MODE` once the reference graph makes
the mode known — a task reached by a workflow node is in node mode.

| Field | Modes |
|---|---|
| `scope` | node, instance |
| `setup`, `cleanup` | node, instance |
| `inner` | node, instance |
| `locals_schema`, `locals_schema_file` | node, instance |
| `inputs_schema`, `inputs_schema_file` | node, instance |
| `outputs_schema`, `outputs_schema_file` | node, instance |
| `outputs` | node, instance |
| `health` | node, instance |
| `terminal` | node |
| `done_when` | instance |
| `requires` | instance |
| `chains` | instance |

`terminal` is node-only because the session's interactive endpoint is resolved
through the node plan, which a dynamic instance is not part of. `done_when`,
`requires`, and `chains` are instance-only because a completion predicate is
evaluated per instance, and a chain fires against instances of the task that
declared it.

The shared portion of the contract dominates: lifecycle, contracts, nesting,
outputs, and health are common, and the disjoint portion is one node-only table
and three instance-only fields. The kind is therefore not split.

`scope` is `run` or `session`, defaulting to `run`. A nested task that declares
none takes the innermost layer's scope.

## Node mode

<!-- fixture: tasks/node-mode.toml -->
```toml
[pane]
kind  = "task"
scope = "run"

[pane.setup]
type = "shell"
script = '''
tmux has-session -t "$session_name" 2>/dev/null \
  || tmux new-session -d -s "$session_name" -c "$workspace_dir"
printf '{"session_name":"%s"}\n' "$session_name"
'''

[pane.setup.bind]
session_name  = { from = "session.name" }
workspace_dir = { from = "workspace.dir" }

[pane.cleanup]
type   = "shell"
script = 'tmux kill-session -t "$session_name" 2>/dev/null || true'

[pane.cleanup.bind]
session_name = { from = "self.session_name" }

[pane.outputs_schema]
type     = "object"
required = ["session_name"]

[pane.outputs_schema.properties]
session_name = { type = "string" }

[pane_session]
kind = "workflow"

[[pane_session.nodes]]
uses = "pane"
```

## Instance mode

<!-- fixture: tasks/instance-mode.toml -->
```toml
[pursue_goal]
kind     = "task"
scope    = "session"
requires = ["goal_parse_status", "goal_status", "checklist_status"]

[pursue_goal.setup]
type = "exec"
bin  = "okf-goal"
args = ["task", "validate-goal-resource", "--resource", { from = "resource.id" }]

[pursue_goal.outputs.bind]
goal_parse_status = { from = "resource.status.goal_parse_status" }
goal_status       = { from = "resource.status.goal_status" }
checklist_status  = { from = "resource.status.checklist_status" }
goal_revision     = { from = "resource.status.goal_revision" }
revision          = { from = "resource.status.revision" }
open_items        = { from = "resource.status.open_items" }

[pursue_goal.outputs_schema]
type = "object"

[pursue_goal.outputs_schema.properties]
goal_parse_status = { type = "string", mutable = true }
goal_status       = { type = "string", mutable = true }
checklist_status  = { type = "string", mutable = true }
goal_revision     = { type = "string", mutable = true }
revision          = { type = "string", mutable = true }
open_items        = { type = "string", mutable = true }

[pursue_goal.done_when]
all = [
  { check = "goal_parse_status", in = ["SUCCESS"] },
  { check = "goal_status", in = ["open"] },
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

[[pursue_goal.chains]]
id        = "goal_review"
workflow  = "goal_review"
placement = "sibling"

[pursue_goal.chains.when]
all = [
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[pursue_goal.chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
```

## Outputs

`[<id>.outputs.bind]` wires the public output contract. Each key is projected
or computed.

Dynamic outputs dissolve into this value model. There is no outputs action
construct and no bulk-copy flag: a task's observed resource state is declared
per key, with renames where the task wants them, and re-evaluation rides on
root liveness rather than on a separate syntax group.

<!-- fixture: observers/per-key-outputs.toml -->
```toml
[review]
kind     = "task"
scope    = "session"
requires = ["kind", "checks"]

[review.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction", "--session", { from = "session.name" }]

[review.outputs.bind]
kind     = { from = "resource.status.resource_kind" }
checks   = { from = "resource.status.checks_status" }
revision = { from = "resource.status.revision" }
pr_url   = { from = "resource.status.pr_url", optional = true }

[review.outputs_schema]
type = "object"

[review.outputs_schema.properties]
kind     = { type = "string", mutable = true }
checks   = { type = "string", mutable = true }
revision = { type = "string", mutable = true }
pr_url   = { type = "string", mutable = true }

[review.done_when]
all = [
  { check = "kind", in = ["pull", "issue"] },
  { check = "checks", in = ["SUCCESS", "NULL"] },
]
```

An output whose value reads a live root is current as of each evaluation. A
direct projection of an inner output keeps write-through; a computed one does
not, so a computed nested output cannot be declared mutable.

## Health

`[<id>.health]` declares this task's contribution to session health: an
`alive` probe and an `activity` probe, each an action.

<!-- fixture: tasks/health.toml -->
```toml
[pane]
kind = "task"

[pane.setup]
type   = "shell"
script = 'printf %s "$session_name"'

[pane.setup.bind]
session_name = { from = "session.name" }

[pane.health.alive]
type   = "shell"
script = 'tmux has-session -t "$session_name"'

[pane.health.alive.bind]
session_name = { from = "self.session_name" }

[pane.health.activity]
type = "shell"
script = '''
PANE=$(tmux capture-pane -p -t "$session_name") || exit 1
FP=$(printf '%s' "$PANE" | cksum | tr -d ' \t')
jq -nc --arg fp "$FP" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{fingerprint: $fp, observed_at: $at}'
'''

[pane.health.activity.bind]
session_name = { from = "self.session_name" }

[pane.outputs_schema]
type     = "object"
required = ["session_name"]

[pane.outputs_schema.properties]
session_name = { type = "string" }
```

## Terminal

`[<id>.terminal]` declares that the task owns an interactive endpoint, offering
`attach`, `capture`, `send_text`, and `send_keys` against it. At most one task
in a plan declares it. `plect attach` and `plect capture` resolve through it,
and the `terminal` capability reaches all four verbs without the consumer
knowing what is behind them.

`send_text` and `send_keys` receive their operand — the literal text to type,
or a key token — as the action's first positional argument.

<!-- fixture: tasks/terminal.toml -->
```toml
[pane]
kind = "task"

[pane.setup]
type   = "shell"
script = 'printf %s "$session_name"'

[pane.setup.bind]
session_name = { from = "session.name" }

[pane.terminal.attach]
type   = "shell"
script = 'tmux attach -t "$session_name"'

[pane.terminal.attach.bind]
session_name = { from = "self.session_name" }

[pane.terminal.capture]
type   = "shell"
script = 'tmux capture-pane -p -t "$session_name"'

[pane.terminal.capture.bind]
session_name = { from = "self.session_name" }

[pane.terminal.send_text]
type   = "shell"
script = 'tmux send-keys -t "$session_name" -- "$1"'

[pane.terminal.send_text.bind]
session_name = { from = "self.session_name" }

[pane.terminal.send_keys]
type   = "shell"
script = 'tmux send-keys -t "$session_name" "$1"'

[pane.terminal.send_keys.bind]
session_name = { from = "self.session_name" }

[pane.outputs_schema]
type     = "object"
required = ["session_name"]

[pane.outputs_schema.properties]
session_name = { type = "string" }

[pane_session]
kind = "workflow"

[[pane_session.nodes]]
uses = "pane"
```

## Nesting

`inner` makes a definition the outer layer of a nesting chain. Inner and outer
tasks are homogeneous, nesting is additive, lifecycle execution is LIFO,
private locals stay layer-local, and inner outputs are never implicitly
promoted into the outer public contract.

The joint reads from the surface being configured: `inner.inputs` is the input
object passed inward, `inner.env` adds environment to the inner task's
executions, and `outputs.bind` projects or computes the outer task's public
outputs.

<!-- fixture: nesting/direct-output.toml -->
```toml
[guarded_runtime]
kind  = "task"
scope = "run"

[guarded_runtime.setup]
type   = "shell"
script = '''
dir=$(mktemp -d)
printf '{"guard_dir":"%s"}\n' "$dir"
'''

[guarded_runtime.locals_schema]
type     = "object"
required = ["guard_dir"]

[guarded_runtime.locals_schema.properties]
guard_dir = { type = "string" }

[guarded_runtime.inner]
uses = "official.claude.runtime"

[guarded_runtime.inner.inputs]
tmux_session = { from = "inputs.tmux_session" }
model        = { from = "inputs.model", default = "fable" }
path_prepend = { from = "locals.guard_dir" }

[guarded_runtime.inner.env]
PLECT_TEAM_CONTEXT = { from = "session.name" }

[guarded_runtime.outputs.bind]
pid         = { from = "inner.outputs.pid" }
socket_path = { from = "inner.outputs.socket_path" }
guard_dir   = { from = "locals.guard_dir" }

[guarded_runtime.outputs_schema]
type     = "object"
required = ["pid", "socket_path"]

[guarded_runtime.outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string", mutable = true }
guard_dir   = { type = "string" }

[guarded_runtime.inputs_schema]
type                 = "object"
required             = ["tmux_session"]
additionalProperties = false

[guarded_runtime.inputs_schema.properties]
tmux_session = { type = "string" }
model        = { type = "string" }
```

A structured record passes through the joint as data and is serialized by the
inner task's own fixed logic — it does not become shell source and does not
ride child argv:

<!-- fixture: nesting/structured-record-passthrough.toml -->
```toml
[team_runtime]
kind  = "task"
scope = "run"

[team_runtime.inner]
uses = "official.claude.runtime"

[team_runtime.inner.inputs]
tmux_session = { from = "nodes.pane.outputs.session_name" }
mcp_servers  = { from = "inputs.mcp_servers", optional = true }

[team_runtime.outputs.bind]
pid         = { from = "inner.outputs.pid" }
socket_path = { from = "inner.outputs.socket_path" }

[team_runtime.outputs_schema]
type = "object"

[team_runtime.outputs_schema.properties]
pid         = { type = "integer", mutable = true }
socket_path = { type = "string", mutable = true }

[team_runtime.inputs_schema]
type                 = "object"
additionalProperties = false

[team_runtime.inputs_schema.properties.mcp_servers]
type = "array"

[team_runtime.inputs_schema.properties.mcp_servers.items]
type                 = "object"
required             = ["name", "command"]
additionalProperties = false

[team_runtime.inputs_schema.properties.mcp_servers.items.properties]
name    = { type = "string" }
command = { type = "string" }
args    = { type = "array", items = { type = "string" } }
env     = { type = "object" }
```

## Completion

`[<id>.done_when]` is a conjunction of leaves plus an optional budget. A check
leaf compares an observed output; a judge leaf waits for independent reviewer
input recorded by `plect judge`, optionally restricted to reviewers in a
declared relation.

`requires` names the output keys those checks read. Every check names a
required output, and every required output is an outputs-schema property, so a
typo in either surfaces at load time.

Omitting `budget` leaves completion unbounded, which is what a long-lived goal
needs: a heartbeat budget is a convergence bound, and continuing to exist is
not exhaustion.

## Validation rules

- A field used outside its modes is a load error.
- Every `done_when` check names a `requires` entry, and every `requires` entry
  is an outputs-schema property.
- A nesting chain that reaches itself is a load error.
- A direct nested projection agrees with the inner output's type and
  mutability.
- A computed nested output is not mutable.
- A nested `outputs.bind` observes inner outputs and this layer's locals only.
- At most one task in a plan declares `terminal`.
