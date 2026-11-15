# Actions

An action is a lifecycle execution: an effect's `setup` or `cleanup`, a health
or terminal probe, a resource's `observe` or `finalize`, a resource query's
`poll` or `subscribe`, and a channel's delivery. Core manages resource
subscriptions without resource subscribe/unsubscribe actions.

Every action declares a `type`: `exec` and `shell` everywhere, and `noop`,
legal only under `[health.alive]`.

## exec

Simple process execution is structural. No shell is involved, so no quoting
question arises.

```toml
[bootstrap]
kind  = "effect"
scope = "run"

[bootstrap.setup]
type = "exec"
bin  = "okf-goal"
args = [
  "task",
  "bootstrap",
  "--owner",
  { from = "inputs.owner" },
  "--session",
  { from = "session.name" },
  "--assignees",
  { from = "inputs.assignees", default = "" },
]

[bootstrap.health.alive]
type = "noop"

[bootstrap.inputs_schema]
type                 = "object"
required             = ["owner"]
additionalProperties = false

[bootstrap.inputs_schema.properties]
owner     = { type = "string" }
assignees = { type = "string" }
```

`args` elements are values: literals, projections, computations, JSON
serializations, or capability tags.
When `bin` names a service-marked executable, an explicit argument may read
its validated non-secret config with `{ from = "service.config" }` or a
declared sub-path. Plect supplies no config environment variable. The same
projection is available in `stdin` and shell `bind` values for that executable.

`stdin` writes one value to the process's standard input instead of its argv.
It exists for data that must not be visible in the process table, and for data
too large to be an argument.

## shell

Plecture keeps shell actions. Real imperative logic — an interactive runtime's
startup and readiness sequence, a generated wrapper script — should stay
inspectable and customizable in configuration rather than disappearing into an
opaque executable purely to avoid template syntax.

Shell source is literal. Plecture and CEL interpolation do not occur inside it.
An action instead declares the values and capabilities it needs outside that
source, in a `bind` table:

<!-- fixture: actions/shell-bind.toml -->
```toml
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type = "shell"
script = '''
"$activity_bin" reset "$session_name"
sh -c "$send_text" terminal-send-text "ready"
sh -c "$send_keys" terminal-send-keys Enter
'''

[runtime.setup.bind]
session_name = { from = "session.name" }
send_text    = { terminal = "send_text" }
send_keys    = { terminal = "send_keys" }
activity_bin = { bin = "codex-agent-activity" }

[runtime.health.alive]
type = "noop"
```

Each `bind` key becomes a shell variable name.

## noop

A noop action runs nothing and exits zero. It declares nothing beyond its
`type`, and it is legal only under `[health.alive]`: everywhere else an action
runs something, so `noop` there is a load error naming the position.

<!-- fixture: effects/health-alive-noop.toml -->
```toml
[write_instruction]
kind  = "effect"
scope = "session"

[write_instruction.setup]
type   = "shell"
script = 'printf %s "$instruction"'

[write_instruction.setup.bind]
instruction = { from = "inputs.instruction" }

[write_instruction.health.alive]
type = "noop"

[write_instruction.inputs_schema]
type                 = "object"
required             = ["instruction"]
additionalProperties = false

[write_instruction.inputs_schema.properties]
instruction = { type = "string" }
```

`noop` means a setup-bearing effect's liveness is deliberately never
re-observed — a systemd `RemainAfterExit=`-style declaration, not the absence
of one. See [`effects.md`](effects.md#health) for the load rule that makes
`[health.alive]` mandatory.

## process

A process action is a liveness check core evaluates itself rather than a
program it runs. It is written as a single key, `process`, naming the effect's
own output that holds the pid of the process its setup started, and it is legal
only under `[health.alive]`.

<!-- fixture: effects/health-alive-process.toml -->
```toml
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type = "exec"
bin  = "agent-runtime"
args = ["launch"]

[runtime.health.alive]
process = "self.outputs.pid"

[runtime.outputs_schema]
type     = "object"
required = ["pid"]

[runtime.outputs_schema.properties]
pid = { type = "integer" }
```

When setup produces the output, core records the process's identity — the pid
and the numeric start time the operating system reports for it — alongside the
instance's outputs. It is core's own record, not an output: it does not appear
in `self.outputs` and no plugin writes it. The probe passes only while the pid
exists and its start time matches the recorded one, so a pid recycled by an
unrelated process fails. Where the platform rounds the start time, one tick of
difference still matches.

A setup whose pid output is absent, is not a process id, or names a process
that is not running fails, since the effect declared that the output names a
live process. An instance recorded without an identity — one set up before the
probe was declared — is judged by pid existence alone.

`process` may also sit beside an exec or shell action. Core's check runs first,
and the action runs only when it passes:

<!-- fixture: effects/health-alive-process-beside-action.toml -->
```toml
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type = "exec"
bin  = "agent-runtime"
args = ["launch"]

[runtime.health.alive]
process = "self.outputs.pid"
type    = "exec"
bin     = "agent-runtime"
args    = ["probe", { from = "self.outputs.state_dir" }]

[runtime.outputs_schema]
type     = "object"
required = ["pid", "state_dir"]

[runtime.outputs_schema.properties]
pid       = { type = "integer" }
state_dir = { type = "string" }
```

For plugin authors: a cleanup script receives the recorded identity as an opaque token,
`{ from = "self.process.identity" }`, absent for an instance recorded without
one (bind it `optional = true`). The script hands the token to
`plect process verify <token>`, which exits zero only while the recorded pid
still belongs to the recorded process. Asking again before each signal is what
keeps a pid recycled during a grace period from being signalled. The
`plect process` command is hidden from `plect --help` because it is a contract
with plugin scripts rather than an operator verb; `plect process verify --help`
still works.

A layer of a nesting chain may declare `process`. Core reads the pid from that
layer's own `self.outputs` — the contract its probes are written against, not
the composed task's — and records one identity for the chain's instance. Only
the declaring layer's cleanup receives `self.process.identity`. A chain in which
two layers declare `process` is a load error.

## The binding transport

Bindings reach the shell process through a private generated binding file, not
through argv and not through rendered shell source.

Plecture writes a mode-0600 binding file, or a generated wrapper, in a private
run directory; exports only file paths and control variables into the process
environment; and executes the literal script by path rather than as
`bash -c <rendered source>`. Bound values therefore never appear in the child
process's argv, and Plecture-owned generation handles shell escaping exactly
once.

The consequence for authors: a value bound into a shell action is data. It
cannot become part of the command that runs, and a script that relays a value
across a further boundary of its own — typing it into a terminal, for
instance — owns the quoting at that boundary.

## bin versus command

`command` is a literal OS command name or path, resolved through the process
environment exactly as an ordinary command is. It is never dynamic.

`bin` resolves a plugin-owned executable and is the preferred form in shipped
plugin config. Inside plugin-mounted config a bare `bin = "name"` resolves
against the containing plugin's own executables. In user-authored config `bin`
takes the catalog-qualified slash form: `<catalog-alias>/<plugin-path>` when
the plugin has one executable, or
`<catalog-alias>/<plugin-path>/<executable-name>` otherwise. An ambiguous
nested plugin-path reading fails loudly rather than guessing.

Shipped plugin config does not use user-local catalog aliases and cannot
reference another plugin's executable.

Because a channel's exec action can name a plugin-owned executable directly,
delivery needs no `PATH`-mangling wrapper:

<!-- fixture: channels/exec-plugin-bin.toml -->
```toml
[exec_delivery]
kind = "channel"
type = "exec"
bin  = "codex-exec-enqueue"
args = [
  { from = "inputs.queue_dir" },
  { from = "inputs.message_envelope" },
  { from = "event.type" },
  { from = "event.body" },
  { from = "event.summary" },
  { from = "event.metadata.url", default = "" },
]
timeout = { from = "inputs.enqueue_timeout", default = "5s" }

[exec_delivery.input_schema]
queue_dir        = { type = "string", required = true }
enqueue_timeout  = { type = "string", default = "5s" }
message_envelope = { type = "string", default = "[{type}] {body_or_summary}{url_suffix}" }
```

An exec action names its executable exactly once, through `bin` or `command`.

## Validation rules

- An action's `type` is `exec` or `shell`, or, under `[health.alive]` only,
  `noop`; `process` is a key rather than a type, legal under `[health.alive]`
  only, alone or beside an exec or shell action.
- `process` names one of the effect's own outputs as `self.outputs.<key>`, and
  no other root.
- An exec action declares exactly one of `bin` and `command`.
- A shell action declares `script`, and no exec-variant field.
- `script` contains no Plecture or CEL interpolation.
- An exec action's `command` is never a computed value.
- A noop action declares nothing beyond `type`.
- A capability tag appears only where that capability is consumable: an action
  binding, or an argv element of an action that accepts one.
- A `terminal` capability requires some effect in the plan to declare that verb.
