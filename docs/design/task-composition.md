# Task composition

This design is governed by
[`../adr/2026-08-17-task-composition.md`](../adr/2026-08-17-task-composition.md).

## Design Core

Task composition lets a user-owned or configuration-only plugin task add
lifecycle work and process environment to a plugin task without copying the
plugin task file.

A composed task has one outer task and one inner task. The outer task is the id
named by a workflow node. The inner task is named by the outer task's `inner`
reference and remains unaware that it is composed.

The lifecycle is LIFO:

```text
outer setup -> inner setup -> inner cleanup -> outer cleanup
```

The composed task exposes the inner task's public outputs. The outer task's
setup outputs are private to the composition layer and are available only to
the outer cleanup and forwarding templates.

## Configuration Shape

An outer task is a normal `tasks/<id>.toml` file with an `inner` reference and
optional forwarding tables:

```toml
inner = "official/claude/claude"

setup = '''
jq -nc --arg guard_dir "{{.Nodes.gh_guard.outputs.dir}}" '{guard_dir:$guard_dir}'
'''
cleanup = "true"

[forward.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Outer.outputs.guard_dir}}"

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
```

The outer task's `inputs_schema` is its own schema. It is not an edit to the
inner task's schema. Workflow node inputs validate against the outer schema,
then `forward.inputs` renders the inner input object and validates it against
the inner task's schema.

The `forward.env` table injects environment variables into process executions
owned by the inner task: setup, cleanup, healthcheck, movement signal, dynamic
output scripts, and terminal operation commands. It does not affect the outer
task's hooks or any sibling task.

Task composition fields:

| Field | Required | Type | Meaning |
|---|---:|---|---|
| `inner` | yes | string | Task reference for the composed inner task. |
| `setup` | no | string | Outer setup hook. Empty means `{}` private outputs. |
| `cleanup` | no | string | Outer cleanup hook. Empty marks the outer layer cleaned. |
| `[forward.inputs]` | no | string table | Templates that produce the inner task's inputs. |
| `[forward.env]` | no | string table | Templates that produce the inner task's process environment additions. |
| `[inputs_schema]` / `inputs_schema_file` | no | JSON Schema | The outer task's workflow-facing inputs contract. |
| `[outputs_schema]` / `outputs_schema_file` | no | JSON Schema | The private outputs contract for outer setup. |
| `[[chains]]` | no | chain entries | Chains attached to the composed task id. |

The composed task's effective scope is the inner task's scope. If the outer
task declares `scope`, it must match the inner task's scope.

## Reference Resolution

`inner` accepts a normal task id or a catalog-qualified task reference:

```toml
inner = "claude"
inner = "official/claude/claude"
```

The normal task id uses the merged task namespace and therefore follows the
usual layer rules. The catalog-qualified form selects a task inside an enabled
plugin without consulting same-id user shadows. It parses the same way as
qualified `{{bin}}` references: plect selects the longest enabled plugin id
matching `<catalog-alias>/<plugin-path>` and treats the remaining segment as
the task id.

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

Loading composed task definitions fails when:

- `inner` names an unknown task, an unknown catalog alias, a disabled plugin, or
  a task missing from the selected plugin.
- `inner` forms a composition cycle, including self-reference.
- the outer task declares a `scope` that differs from the inner task's scope.
- the outer task declares inner-owned behavior fields: `primary`, `execution`,
  `healthcheck`, `movement_signal`, `[terminal]`, `[done_when]`, or
  `[[outputs]]`.
- `forward.inputs` omits an inner required input or forwards a key rejected by a
  closed inner schema.
- a `forward.env` key is not a valid process environment name.

Running a composed task fails when:

- the outer setup exits non-zero or emits outputs rejected by the outer private
  outputs schema;
- a forwarding template fails to render;
- rendered forwarded inputs fail the inner task's schema value validation;
- any inner task process fails under the normal inner task rules.

Cleanup skips layers that never reached setup. If the inner setup fails after
the outer setup succeeds, the next cleanup unwinds the produced outer layer.

## Customization Ladder

Task composition is the middle rung:

1. Parameterization: a plugin task author declares a supported input.
2. Task composition: an outer task adds lifecycle, env, chains, and input
   forwarding.
3. Fork: a user copies the whole task because the required behavior is not an
   author-declared variation point and cannot be added outside the task.

Workflow node inputs remain the home for user default values. Script-internal
variation remains parameterization work for the plugin task author.

## Worked Example

A team-local Claude runtime customization can stay small:

```toml
inner = "official/claude/claude"
setup = "jq -nc --arg guard_dir '{{.Nodes.gh_guard.outputs.dir}}' '{guard_dir:$guard_dir}'"

[forward.inputs]
tmux_session = "{{.Inputs.tmux_session}}"
model = "{{get .Inputs \"model\"}}"
effort = "{{get .Inputs \"effort\"}}"
path_prepend = "{{.Outer.outputs.guard_dir}}"

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
```

The same pattern covers task-shaped shadows whose differences are input values,
extra agent-process environment, chain declarations, or a single command path
expressed as an environment/path input. Workspace layout belongs to the
workspace provider or workflow node inputs. Script-internal command changes
belong to parameterization; if no author-declared variation point exists, a
fork remains the explicit last resort.
