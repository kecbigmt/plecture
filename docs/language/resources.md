# Resources

A resource declaration owns one kind of external thing: recognition, resource
naming, observation, query, subscription limits, and finalization. A resource is
not a workflow node and does not acquire an environment.

## Identity and actions

`match` recognizes a concrete identifier; `name` derives that resource's
resource name from its captures. The resource name is the resource's identity:
identifiers that derive one name, such as two spellings of one link, are one
resource, and a session or task bound to it gives its actions the identifier
first resolved to that resource as `resource.id`. Resource names are unique
across resource types, because a session takes its name from one; a resource
name is not itself a session name, which may append a tag.
`observe` refreshes `resource.state.*`; it neither performs work nor implies
completion. `finalize` records a task's accepted completion and its evidence.
Core binds delivery with a subscription row for each session/resource pair.
A resource may declare `max_subscribers` as a positive integer; a subscribe
that exceeds it fails without adding a row. Resource action inputs are never
inferred from workflow inputs or a previous resource binding. Query inputs are
separate: a population supplies the literal object required by
`[resource.query.inputs_schema]`, and query actions see it as `inputs.*`, not
`resource.inputs.*`.

```toml
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
name  = { expr = "match.project + '-issue-' + match.number" }

[issue.observe]
type = "exec"
bin  = "forge"
args = ["issue", "observe", { from = "resource.id" }]

[issue.state_schema]
type = "object"

[issue.state_schema.properties]
revision = { type = "string" }
pr_url   = { type = "string" }
```

The first observation is required before instantiation; later observation
failure is recorded degradation. Finalization receives the concrete resource
and session. A resource does not own a task's state or judge evidence.

`plect subscribe <resource-id>` resolves exactly one resource declaration and
records an idempotent `runtime` row for the calling session. It runs no
resource hook. `plect unsubscribe <id>` removes only that session's `runtime`
row for resource `<id>`. `plect up <url>` records a `dispatch` row for the
session's entry resource. A task instance bound to a resource records a `task`
row until the last task in that session bound to it is cleaned up. A node with
`subscription` records a `node` row while any live node binds the resource.
Task binding and subscription admission, or cleanup and removal, use one
database transaction. Source rows overlap without replacing one
another. Subscriptions survive session down/up. Destroying a session removes
them; they also cascade if its retained session row is deleted. Core records
`inputs` JSON and `created_at` for each row. A `max_subscribers` limit counts
distinct sessions, not rows. `plect event publish --resource <id>` appends an
event once to each subscribed session log and reports the delivered session
count. A failed append is an error, not zero delivery. Services receive the
full membership snapshot and later deltas from their supervising plect over
stdio; `plect subscription list [--session] [--resource-type] --json` is the
operator-facing query.

The Slack bot-user resource form is `slack://<team_id>/<bot_user_id>` and sets
`max_subscribers = 1`. A session that handles otherwise-undelivered mentions
subscribes explicitly. The adapter publishes allowed mentions as `user.emit`
by resource identifier; core routes them using its subscription table.

## Query

`[<id>.query]` finds resource identifiers for a workflow population. It
declares an input schema and an item schema, and at least one of `poll` or
`subscribe`. A successful poll is the only absence authority; subscribe items
only report appearances. A query item has a string `resource` property and
optional appearance context. It does not duplicate observation state.

## Validation rules

- `match`, `name`, `observe`, and `state_schema` are required.
- A `name` projection names only captures declared by `match`.
- `name` derives a value matching the session-name rule
  `[A-Za-z0-9][A-Za-z0-9/_+-]*`; any other value is an error where it is
  derived.
- A resource name another resource type already holds is an error where an
  identifier resolves to it.
- `max_subscribers`, when present, is a positive integer.
- Query inputs satisfy the query input schema independently of action inputs.
- A query has its shared schemas and at least one means.
- A task's resource type matches the concrete resource to which its instance
  is bound.
