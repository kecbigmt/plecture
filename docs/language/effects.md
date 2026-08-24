# Effects

An effect is a lifecycle-managed provider: contracts, health, an optional
interactive capability, and composition.

It brings something up, keeps it observable, and takes it down. It does not
decide whether the work is finished — that is a [task document](tasks.md).

## Surface

| Field | Meaning |
|---|---|
| `scope` | `run` or `session`, defaulting to `run`. |
| `setup`, `cleanup` | The lifecycle actions. See [`actions.md`](actions.md). |
| `inputs_schema`, `outputs_schema` | The effect's contracts. |
| `[health]` | The `alive` and `activity` probes. |
| `[terminal]` | The interactive endpoint, if this effect owns one. |
| `[setup.process]` | The effect's long-lived process, if it starts one. |
| `inner`, `[inner.inputs]`, `[inner.env]`, `[outputs.bind]`, `locals_schema` | The nesting joint. |

That is the whole grammar. A completion predicate, the keys it reads, its
budget, and the workflows it spawns all belong to a task document.

<!-- fixture: effects/lifecycle.toml -->
```toml
[pane]
kind  = "effect"
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
session_name = { from = "self.outputs.session_name" }

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

A nested effect that declares no `scope` takes the innermost layer's scope.

## Outputs are production records

An effect's outputs are facts it produced: what setup wrote to stdout, and what a
nesting joint projected outward. They are records of what happened, not a view
of something that keeps changing.

So `[outputs.bind]` reads `inner.outputs.*`, `locals.*`, and this layer's own
`inputs.*` — every one of them fixed when the layer is instantiated. What it
cannot read is a live root: that belongs to a task document's completion
predicate, and re-evaluation semantics exist only there.

<!-- fixture: effects/live-root-output.invalid.toml -->
```toml
[render]
kind  = "effect"
scope = "session"

[render.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[render.outputs.bind]
checks_status = { from = "resource.state.checks_status" }

[render.outputs_schema]
type = "object"

[render.outputs_schema.properties]
checks_status = { type = "string" }
```

## Health

`[health]` declares this effect's contribution to session health: an `alive`
probe and an `activity` probe, each an action.

<!-- fixture: effects/health.toml -->
```toml
[pane]
kind = "effect"

[pane.setup]
type   = "shell"
script = 'printf %s "$session_name"'

[pane.setup.bind]
session_name = { from = "session.name" }

[pane.health.alive]
type   = "shell"
script = 'tmux has-session -t "$session_name"'

[pane.health.alive.bind]
session_name = { from = "self.outputs.session_name" }

[pane.health.activity]
type = "shell"
script = '''
PANE=$(tmux capture-pane -p -t "$session_name") || exit 1
FP=$(printf '%s' "$PANE" | cksum | tr -d ' \t')
jq -nc --arg fp "$FP" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{fingerprint: $fp, observed_at: $at}'
'''

[pane.health.activity.bind]
session_name = { from = "self.outputs.session_name" }

[pane.outputs_schema]
type     = "object"
required = ["session_name"]

[pane.outputs_schema.properties]
session_name = { type = "string" }
```

## Terminal

`[terminal]` declares that the effect owns an interactive endpoint, offering
`attach`, `capture`, `send_text`, and `send_keys` against it. At most one effect
in a plan declares it. The CLI's attach and capture commands resolve through
it, and the `terminal` capability reaches all four verbs without the consumer
knowing what is behind them.

`send_text` and `send_keys` receive their operand — the literal text to type,
or a key token — as the action's first positional argument.

<!-- fixture: effects/terminal.toml -->
```toml
[pane]
kind = "effect"

[pane.setup]
type   = "shell"
script = 'printf %s "$session_name"'

[pane.setup.bind]
session_name = { from = "session.name" }

[pane.terminal.attach]
type   = "shell"
script = 'tmux attach -t "$session_name"'

[pane.terminal.attach.bind]
session_name = { from = "self.outputs.session_name" }

[pane.terminal.capture]
type   = "shell"
script = 'tmux capture-pane -p -t "$session_name"'

[pane.terminal.capture.bind]
session_name = { from = "self.outputs.session_name" }

[pane.terminal.send_text]
type   = "shell"
script = 'tmux send-keys -t "$session_name" -- "$1"'

[pane.terminal.send_text.bind]
session_name = { from = "self.outputs.session_name" }

[pane.terminal.send_keys]
type   = "shell"
script = 'tmux send-keys -t "$session_name" "$1"'

[pane.terminal.send_keys.bind]
session_name = { from = "self.outputs.session_name" }

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

## Process

`[<id>.setup.process]` declares the effect's one long-lived process — an
agent's event loop, for instance — and submits it into the plan's declared
`[terminal]` endpoint. An effect that starts no long-lived process omits it.

It is a sub-step of `setup`, not a lifecycle table of its own: within one
`setup` action, the action's own `script` or `exec` body runs first and
produces this layer's setup outputs, and only then does core resolve and
submit the process step. A downstream node that depends on this node's setup
output is scheduled only after that submission, so a later setup step —
delivering an agent's first prompt into an already-live pane, for instance —
may assume the process has already received its launch line.

| Field | Required | Meaning |
|---|---:|---|
| `command` | yes | The text submitted into the endpoint. Typically `{ from = "self.outputs.command" }`, sound because the process step runs after the script that produced it. |
| `env` | no | Literal environment assignments, keyed by `^[A-Za-z_][A-Za-z0-9_]*$`. Each value is resolved once and never expanded — a value containing `$PATH` types out as that literal text, not a shell expansion. |
| `path` | no | `prepend` and/or `append`, each a value producing a directory. Both may be present; core applies them in a fixed order, prepend then append. |

```toml
[runtime]
kind = "effect"

[runtime.setup]
type   = "shell"
script = '''
SID=$(uuidgen)
jq -nc --arg sid "$SID" --arg cmd "claude --session-id $SID" \
  '{session_id:$sid, command:$cmd}'
'''

[runtime.setup.process]
command = { from = "self.outputs.command" }

[runtime.setup.process.env]
PLECT_SESSION_NAME = { from = "session.name" }

[runtime.setup.process.path]
prepend = { from = "inputs.path_prepend", optional = true }

[runtime.inputs_schema]
type                 = "object"
additionalProperties = false

[runtime.inputs_schema.properties]
path_prepend = { type = "string" }

[runtime.outputs_schema]
type     = "object"
required = ["session_id", "command"]

[runtime.outputs_schema.properties]
session_id = { type = "string" }
command    = { type = "string" }

[runtime.terminal.send_text]
type   = "shell"
script = 'tmux send-keys -t "$session_id" -- "$1"'

[runtime.terminal.send_text.bind]
session_id = { from = "self.outputs.session_id" }

[runtime.terminal.send_keys]
type   = "shell"
script = 'tmux send-keys -t "$session_id" "$1"'

[runtime.terminal.send_keys.bind]
session_id = { from = "self.outputs.session_id" }
```

Composing the launch line — quoting `env` values, applying the `path`
operations, and appending `command` — and submitting it through the
endpoint's `send_text` followed by `send_keys` (`Enter`) is core's job. A
setup script never builds or sends this line itself; it produces only the
data `command`, `env`, and `path` are made from. This keeps quoting and
key-name validation a data-time check, before any keystroke is sent, on the
same footing as a shell action's `bind` values never reaching rendered
source (see [`actions.md`](actions.md)).

## Nesting

`inner` makes a definition the outer layer of a nesting chain. Inner and outer
effects are homogeneous, nesting is additive, lifecycle execution is LIFO,
private locals stay layer-local, and inner outputs are never implicitly
promoted into the outer public contract.

The joint reads from the surface being configured: `inner.inputs` is the input
object passed inward, `inner.env` adds environment to the inner effect's
executions, and `outputs.bind` projects or computes the outer effect's public
outputs.

<!-- fixture: nesting/direct-output.toml -->
```toml
[guarded_runtime]
kind  = "effect"
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
Nesting is an effect concept only — a task composes in document vocabulary,
never through this joint.

A structured record passes through the joint as data and is serialized by the
inner effect's own fixed logic — it does not become shell source and does not
ride child argv:

<!-- fixture: nesting/structured-record-passthrough.toml -->
```toml
[team_runtime]
kind  = "effect"
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

- The effect grammar is closed: a completion field is not part of it.
- `[outputs.bind]` reads `inner.outputs.*`, `locals.*`, or `inputs.*`, and no
  live root.
- A nesting chain that reaches itself is a load error.
- A direct nested projection agrees with the inner output's type and
  mutability.
- A computed nested output is not mutable.
- At most one effect in a plan declares `[terminal]`.
- A dynamic-instantiation target resolving to an effect is a kind mismatch:
  instantiation creates a task instance from a task document.
- At most one layer of a nesting chain declares `[setup.process]`, mirroring
  the `[terminal]` per-chain rule above.
- `[setup.process]` requires some effect in the plan to declare `[terminal]`.
- `[setup.process]`'s `command` is required and resolves to a string.
- A `[setup.process.env]` key that does not match `^[A-Za-z_][A-Za-z0-9_]*$`
  is rejected.
- A `[setup.process.env]` value, or a `path.prepend`/`path.append` value, that
  resolves to anything but a scalar is rejected.
- `[setup.process.path]`'s only keys are `prepend` and `append`.
