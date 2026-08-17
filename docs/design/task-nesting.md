# Task nesting

This design is governed by
[`../adr/2026-08-17-task-nesting.md`](../adr/2026-08-17-task-nesting.md).

## Design Core

Task nesting lets a user-owned or configuration-only plugin task add
lifecycle work, input forwarding, chains, and process environment to a plugin
task without copying the plugin task file.

A nested task has a chain of task definitions. The outermost task is the id
named by a workflow node. Each outer task names its next inner task with
`inner`, and that inner task may itself be nested. The innermost task remains
unaware that it is nested.

The lifecycle is an N-layer LIFO stack:

```text
outermost setup -> ... -> innermost setup -> innermost cleanup -> ... -> outermost cleanup
```

The nested task exposes only the public outputs the outer task explicitly
wires. Inner public outputs are not passed through automatically. The outer
task may re-export inner outputs, rename inner outputs, or publish values
computed by its own setup, but downstream consumers bind only to the outer
task's declared public contract. Outer setup emits locals: always-private
intermediate values available to outer cleanup, forwarding templates, and
public output wiring.

## Configuration Shape

An outer task is a normal `tasks/<id>.toml` file with an `inner` reference and
optional forwarding tables:

```toml
inner = "official/claude/claude"

setup = '''
jq -nc --arg guard_dir "{{.Nodes.gh_guard.outputs.dir}}" '{guard_dir:$guard_dir}'
'''
cleanup = "true"

expose = ["pid", "session_id", "socket_path", "mcp_config"]

[forward.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Locals.guard_dir}}"

[forward.env]
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
```

The outer task's `inputs_schema` is its own schema. It is not an edit to the
inner task's schema. Workflow node inputs validate against the outer schema,
then `forward.inputs` renders the inner input object and validates it against
the inner task's schema.

The `forward.env` table injects environment variables into process executions
owned by the inner task: setup, cleanup, healthcheck, movement signal, dynamic
output scripts, and terminal operation commands. It does not affect the outer
task's hooks, any sibling task, or commands the inner task sends into an
interactive endpoint. Agent runtimes that launch by typing into a terminal
therefore need an author-declared input for launch-line environment exports.

Task nesting fields:

| Field | Required | Type | Meaning |
|---|---:|---|---|
| `inner` | yes | string | Task reference for the nested inner task. |
| `setup` | no | string | Outer setup hook. Empty means `{}` locals. |
| `cleanup` | no | string | Outer cleanup hook. Empty marks the outer layer cleaned. |
| `expose` | no | string array | Same-name re-export shorthand for inner public outputs. |
| `[forward.inputs]` | no | string table | Templates that produce the inner task's inputs. |
| `[forward.env]` | no | string table | Templates that produce the inner task's process environment additions. |
| `[outputs]` | no | string table | Templates that wire the nested task's public outputs from inner outputs or locals. Renaming is explicit here. |
| `[inputs_schema]` / `inputs_schema_file` | no | JSON Schema | The outer task's workflow-facing inputs contract. |
| `[locals_schema]` / `locals_schema_file` | no | JSON Schema | The private locals contract for outer setup emissions. |
| `[outputs_schema]` / `outputs_schema_file` | no | JSON Schema | The nested task's explicit public output contract. Same-name `expose` entries import their inner schema. |
| `[[chains]]` | no | chain entries | Chains attached to the nested task id, validated against the outer public done_when and output contract. |

The nested task's effective scope is the innermost task's scope. If an outer
task declares `scope`, it must match the scope of its next inner task, and the
rule repeats down the nesting chain.
The inner task's `.Self` sees only the inner task's own outputs. Outer cleanup,
forwarding templates, and `[outputs]` wiring read outer setup values from
`.Locals`; `[outputs]` wiring also reads inner setup values from
`.Inner.outputs`.

Inspection output such as `plect task show` prints the nesting chain from the
outermost task to the innermost plugin task.

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

## Validation Rules

Loading nested task definitions fails when:

- `inner` names an unknown task, an unknown catalog alias, a disabled plugin, or
  a task missing from the selected plugin.
- `inner` forms a nesting cycle, including self-reference.
- the outer task declares a `scope` that differs from the inner task's scope.
- the outer task declares inner-owned behavior fields: `primary`, `execution`,
  `idle_after`, `healthcheck`, `movement_signal`, `requires`, `[terminal]`,
  `[done_when]`, or `[[outputs]]`.
- `expose` names an output not declared by the inner task.
- `[outputs]` references a source other than an inner public output or a local.
- the same public output key is declared by both `expose` and `[outputs]`.
- `forward.inputs` omits an inner required input or forwards a key rejected by a
  closed inner schema.
- a `forward.env` key is not a valid process environment name.
- a `forward.env` key repeats a key from any other layer in the nesting chain.
- a chain declared on the outer task references a judge id or public output not
  declared by the outer public contract.
- a chain declared on the outer task references a local not wired into the outer
  public contract.
- the inner task declares `[terminal]` and the outer public contract does not
  expose `interactive_endpoint`.
- a nested task `done_when` or chain references an output not exposed by the
  outer public contract.

Running a nested task fails when:

- the outer setup exits non-zero or emits locals rejected by the locals schema;
- a forwarding template fails to render;
- rendered forwarded inputs fail the inner task's schema value validation;
- a public output wiring template fails to render or emits a value rejected by
  the public outputs schema;
- any inner task process fails under the normal inner task rules.

Cleanup skips layers that never reached setup. If an inner setup fails after an
outer setup succeeds, the next cleanup unwinds each produced outer layer.

## Customization Ladder

Task nesting is the middle rung:

1. Parameterization: a plugin task author declares a supported input.
2. Task nesting: an outer task adds lifecycle, locals, env, chains, input
   forwarding, and explicit output wiring.
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
mapping. Explicit outer-output wiring applies to task definitions; workspace,
resource, and channel shadows resolve through plugin parameterization or
adoption in their owning config kinds.

| Shadow | Durable intent | Rung | Zero-copy path |
|---|---|---:|---|
| `tasks/claude.toml` | Different runtime inputs, extra agent-process env, path injection, chains. | 1 + 2 | Rung 2 forwards `tmux_session`/model/effort/path inputs and declares chains on the outer id. Rung 1 in the `claude` plugin provides an author-declared `launch_env` input whose exports are included in the terminal launch line. Workflow-owned defaults stay in the workflow node. |
| `tasks/codex.toml` | Different runtime inputs, extra agent-process env, path injection, chains. | 1 + 2 | Rung 2 forwards the plugin task inputs and declares chains. Rung 1 in the `codex` plugin provides an author-declared `launch_env` input whose exports are included in the Codex TUI launch line. |
| `tasks/codex_exec.toml` | Different runtime inputs, extra worker-process env, path injection, worker state location. | 1 + 2 | Rung 2 forwards runtime/path inputs and declares chains. Rung 1 in the `codex` plugin provides author-declared `launch_env` and `state_root` inputs for the worker's exported environment and state directory. |
| `tasks/work.toml` | Different instruction input, explicit output boundary, and local chain attachment. | 2 | The outer task forwards `instruction` into `official/github/work`, re-exports the inner outputs it chooses to publish, wires any local computed output through `[outputs]`, and attaches chains to the outer id. Workflow-owned defaults stay in the workflow node. |
| `channels/codex_exec.toml` | Queue timeout and message formatting around the shipped enqueue executable. | 1 | The channel adopts the plugin's shipped enqueue executable. Rung 1 in the `codex` plugin exposes `enqueue_timeout` and `message_envelope`; `message_envelope` is a formatting template over the existing event fields, not executable substitution. |
| `workspaces/github.toml` | Workspace layout root, branch naming, cleanup policy, and local-only status outputs. | 1 | Rung 1 in the `github` plugin exposes `workspace_layout_root`, `issue_branch_template`, `tagged_branch_suffix`, and `delete_branch_default`. The plugin already owns `title`; the local-only `checks_status` output is dropped from workspace setup because resource observation owns CI status. |
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

expose = ["pid", "session_id", "socket_path", "mcp_config"]

[forward.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Locals.guard_dir}}"

[forward.env]
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
```

The same pattern covers task-shaped shadows whose differences are input values,
chain declarations, explicit output publication, or a single command path
expressed as an environment/path input. Agent-process environment for
terminal-launched runtimes belongs to author-declared launch inputs on the
runtime plugin. Workspace layout belongs to the workspace provider or workflow
node inputs. Script-internal command changes belong to parameterization; if no
author-declared variation point exists, a fork remains the explicit last
resort.
