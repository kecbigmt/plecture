# Resource observers

A resource observer applies the generic resource contract to one kind of
external resource: how to recognize it, how to observe its state, and — when
the resource is something a session finishes — how to record that completion.

An observer is independent of any workspace provider. It consolidates one
observation into a standalone contract, callable on its own, and it is what a
task document's `resource.state.*` reads resolve against.

## Surface

| Field | Meaning |
|---|---|
| `match` | Regular expression recognizing the resource identifier. |
| `observe` | Action producing the resource's current state. |
| `finalize` | Action recording completion and its judge evidence. |
| `query` | Optional shared contract with `poll` and/or `subscribe` item sources. |
| `state_schema` | JSON Schema contract for the observed state. |

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

## State and task

`state_schema` is the contract a task document reads. A task document declares
the observer it is written for, and its completion leaves and chains then read
that observer's keys as `resource.state.<key>` — directly, with no intermediate
declaration re-listing them. Effects read nothing here: `resource.state.*` is task
vocabulary, and an effect's outputs are its own production records.

`resource.state.*` is a live root, so every read is current as of that
evaluation, and one observer serves a standalone status read and every instance
alike.

### Error semantics

An `observe` action's exit code signals health and its stdout contributes state,
and the consequence of a non-zero exit depends on when it happens.

| When | Consequence |
|---|---|
| The first observation, at instantiation | Instantiation is rejected with this error. No instance is created. |
| Any later observation | Recorded degradation. The instance survives; its state reads as unobserved until the next success. |

This is why domain validation belongs here rather than in a lifecycle action.
The observer is the thing that knows what the resource is, so an unparseable
file or an absent record is its answer to give — and giving it at the first
observation means a task instance never exists in a state its own resource
cannot support. A partial answer is different from an error: an observer that
reaches its resource but finds it malformed reports that as state, in a status
key, and exits zero.

Because the observer is named in the document, a key this `state_schema` does
not declare is a load error, `PLECTURE-CFG-FROM-PATH`.

## Query

`[<id>.query]` finds resources of the observer's kind. Its required
`inputs_schema` describes the literal parameter object supplied by a workflow
population, and its required `item_schema` describes every item produced by
either means. At least one means is present:

- `poll` runs to completion and returns a JSON array containing the complete
  matching set. Only a successful, fully validated poll proves absence.
- `subscribe` stays supervised and emits one JSON object per line. An item
  reports an appearance; silence, failure, and restart never prove absence.

The item schema has type `object`, requires only a string `resource`, and may
declare optional identity or appearance context. Query items do not duplicate
properties from `state_schema`.

The three faces answer separate questions: `observe` answers what a resource's
state is, `query.poll` answers which resources match now, and
`query.subscribe` answers when one appears. `observe` remains the sole source
of live `resource.state.*` facts.

<!-- fixture: observers/query.toml -->
```toml
[query_source]
kind  = "resource_observer"
match = '^urn:case:[A-Za-z0-9]+$'

[query_source.observe]
type    = "exec"
command = "observe-case"
args    = [{ from = "resource.id" }]

[query_source.state_schema]
type = "object"

[query_source.state_schema.properties]
phase = { type = "string" }

[query_source.query.inputs_schema]
type                 = "object"
required             = ["scope"]
additionalProperties = false

[query_source.query.inputs_schema.properties]
scope = { type = "string" }

[query_source.query.item_schema]
type                 = "object"
required             = ["resource"]
additionalProperties = false

[query_source.query.item_schema.properties]
resource = { type = "string" }
context  = { type = "string" }

[query_source.query.poll]
type    = "exec"
command = "query-cases"
args    = ["--scope", { from = "inputs.scope" }]

[query_source.query.subscribe]
type    = "exec"
command = "subscribe-cases"
args    = ["--scope", { from = "inputs.scope" }]
```

## Validation rules

- `observe` is declared; `finalize` is optional.
- A query declares both shared schemas and at least one of `poll` and
  `subscribe`.
- A query item requires only its string `resource` property and declares no
  property also declared by `state_schema`.
- A non-zero `observe` exit rejects instantiation at the first observation, and
  is recorded degradation at every later one.
- A `resource.state.<key>` projection names a `state_schema` property.
- A `match` that no resource identifier can satisfy is still a valid
  definition; recognizing nothing is not a load error.
