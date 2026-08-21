# Resource observers

A resource observer applies the generic resource contract to one kind of
external resource: how to recognize it, how to observe its state, and — when
the resource is something a session finishes — how to record that completion.

An observer is independent of any workspace provider. It consolidates one
observation into a standalone contract, callable outside any task instance, and
it is what a task's `resource.state.*` projections read.

## Surface

| Field | Meaning |
|---|---|
| `match` | Regular expression recognizing the resource identifier. |
| `observe` | Action producing the resource's current self. |
| `finalize` | Action recording completion and its judge evidence. |
| `state_schema` | JSON Schema contract for the observed self. |

<!-- fixture: observers/observe-finalize.toml -->
```toml
[goal]
kind  = "resource_observer"
match = '^local-okf://[A-Za-z0-9][A-Za-z0-9-]*/goals/[A-Za-z0-9._/-]+\.md$'

[goal.observe]
type = "exec"
bin  = "okf-goal"
args = ["resource", "observe", "--resource", { from = "resource.id" }]

[goal.finalize]
type = "exec"
bin  = "okf-goal"
args = [
  "resource",
  "finalize",
  "--resource",
  { from = "resource.id" },
  "--revision",
  { from = "resource.revision" },
]
stdin = { json = { from = "judges" } }

[goal.state_schema]
type     = "object"
required = ["goal_parse_status", "goal_status", "checklist_status", "goal_revision", "open_items"]

[goal.state_schema.properties]
goal_parse_status = { type = "string", enum = ["SUCCESS", "FAILURE", "UNRESOLVED"] }
goal_status       = { type = "string", enum = ["open", "blocked", "completed", "archived", "NULL"] }
checklist_status  = { type = "string", enum = ["SUCCESS", "PENDING", "NULL"] }
goal_revision     = { type = "string" }
revision          = { type = "string" }
open_items        = { type = "string" }
observe_error     = { type = "string" }
```

`finalize` runs after completion was already reconfirmed and judge evidence
gathered, so it records rather than gates. Judge evidence arrives on the
process's standard input: judge reasons are arbitrary text, and argv is both
size-bounded and readable by anything that can see the process table.

## State and tasks

A work document observes state per key, renaming where it wants to, so a
standalone status read and every instance read one contract.
`resource.state.*` is a live root, so those projections are current as of each
evaluation.

A projection naming a key the observer's `state_schema` does not declare is
`PLECTURE-CFG-FROM-PATH`.

## Validation rules

- `observe` is declared; `finalize` is optional.
- A `resource.state.<key>` projection names a `state_schema` property.
- A `match` that no resource identifier can satisfy is still a valid
  definition; recognizing nothing is not a load error.
