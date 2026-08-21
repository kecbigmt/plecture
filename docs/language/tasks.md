# Tasks

A task is a lifecycle-managed provider: contracts, health, an optional
interactive capability, and composition.

It brings something up, keeps it observable, and takes it down. It does not
decide whether work is finished — that is a [work document](work.md).

## Surface

| Field | Meaning |
|---|---|
| `scope` | `run` or `session`, defaulting to `run`. |
| `setup`, `cleanup` | The lifecycle actions. See [`actions.md`](actions.md). |
| `inputs_schema`, `outputs_schema` | The task's contracts. |
| `[health]` | The `alive` and `activity` probes. |
| `[terminal]` | The interactive endpoint, if this task owns one. |
| `inner`, `[inner.inputs]`, `[inner.env]`, `[outputs.bind]`, `locals_schema` | The nesting joint. |

That is the whole grammar. A completion predicate, the keys it reads, its
budget, and the workflows it spawns all belong to a work document.

<!-- fixture: tasks/lifecycle.toml -->
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

A nested task that declares no `scope` takes the innermost layer's scope.

## Outputs are production records

A task's outputs are facts it produced: what setup wrote to stdout, and what a
nesting joint projected outward. They are records of what happened, not a view
of something that keeps changing.

So `[outputs.bind]` projects `inner.outputs.*` and `locals.*` — nothing else. A
live root belongs to a work document's `observe`, and re-evaluation semantics
exist only there.

<!-- fixture: tasks/live-root-output.invalid.toml -->
```toml
[render]
kind  = "task"
scope = "session"

[render.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[render.outputs.bind]
checks_status = { from = "resource.status.checks_status" }

[render.outputs_schema]
type = "object"

[render.outputs_schema.properties]
checks_status = { type = "string" }
```

## Health

`[health]` declares this task's contribution to session health: an `alive`
probe and an `activity` probe, each an action.

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

`[terminal]` declares that the task owns an interactive endpoint, offering
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

Write-through is a property of this joint: a direct projection of a mutable
inner output carries later writes outward, and a computed binding does not.
Nesting is a task concept only — work composes in document vocabulary, never
through this joint.

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

## Validation rules

- The task grammar is closed: a completion field is not part of it.
- `[outputs.bind]` projects `inner.outputs.*` or `locals.*`.
- A nesting chain that reaches itself is a load error.
- A direct nested projection agrees with the inner output's type and
  mutability.
- A computed nested output is not mutable.
- At most one task in a plan declares `[terminal]`.
- A `plect task setup` target resolving to a task is a kind mismatch: dynamic
  instantiation targets work documents.
