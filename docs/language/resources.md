# Resources

A resource declaration owns one kind of external thing: recognition, instance
naming, observation, query, delivery binding, and finalization. A resource is
not a workflow node and does not acquire an environment.

## Identity and actions

`match` recognizes a concrete identifier; `name` derives that resource
instance's name from its captures. This name is distinct from a session name.
`observe` refreshes `resource.state.*`; it neither performs work nor implies
completion. `finalize` records a task's accepted completion and its evidence.
`subscribe` and `unsubscribe` bind or remove delivery for one session/resource
pair.

`subscribe` and `unsubscribe` take a resource input object satisfying
`inputs_schema` when the resource declares one; no declaration means the
empty object. A binding made by `plect up`, `plect task setup`, a chain, a
population, or a node's `subscription` passes the empty object. Only a
standalone `plect subscribe` or unsubscribe supplies one.

Resource action inputs are never inferred from workflow inputs or a previous
resource binding. Query inputs are separate: a population supplies the literal
object required by `[resource.query.inputs_schema]`, and query actions see it
as `inputs.*`, not `resource.inputs.*`.

```toml
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
name  = { expr = "match.project + '-issue-' + match.number" }

[issue.observe]
type = "exec"
bin  = "forge"
args = ["issue", "observe", { from = "resource.id" }]

[issue.subscribe]
type = "exec"
bin  = "forge"
args = ["issue", "subscribe", { from = "resource.id" }, { from = "session.name" }, "--events", { from = "resource.inputs.events", default = "all" }]

[issue.inputs_schema]
type = "object"

[issue.inputs_schema.properties]
events = { type = "string", enum = ["all", "comments"] }

[issue.state_schema]
type = "object"

[issue.state_schema.properties]
revision = { type = "string" }
pr_url   = { type = "string" }
```

The first observation is required before instantiation; later observation
failure is recorded degradation. Finalization receives the concrete resource
and session; delivery also receives the resource action input object. A
resource does not own a task's state or judge evidence.

`plect subscribe <resource-id>` selects exactly one matching resource
declaration. It accepts `--inputs` or `--inputs-file` for the action input
object. Session creation and task binding subscribe automatically when that
declaration has a `subscribe` action and the empty input object satisfies its
schema. A node's `subscription` uses the same empty-input rule after its setup
produces an identifier. If required inputs prevent that binding, a known
identifier fails loading and a runtime identifier reports a subscription
failure for retry; neither is silently skipped. Teardown runs `unsubscribe`
when the resource is no longer needed by the session entry, any task, or any
live node.

## Query

`[<id>.query]` finds resource identifiers for a workflow population. It
declares an input schema and an item schema, and at least one of `poll` or
`subscribe`. A successful poll is the only absence authority; subscribe items
only report appearances. A query item has a string `resource` property and
optional appearance context. It does not duplicate observation state.

## Validation rules

- `match`, `name`, `observe`, and `state_schema` are required.
- A `name` projection names only captures declared by `match`.
- Every subscribe and unsubscribe receives an object satisfying
  `inputs_schema`.
- Query inputs satisfy the query input schema independently of action inputs.
- A query has its shared schemas and at least one means.
- A task's resource type matches the concrete resource to which its instance
  is bound.
