# Chains

A chain is a deterministic rule for spawning work off a work document's
instances: once this instance reaches this state, run that workflow.

Chains live in the declaring work document's frontmatter because a chain's
`when` judge ids and `inputs` projections are references into that same
document's completion contract and observed keys — colocation is what lets
those references be checked at load time rather than at fire time. Evaluation
is scoped the same way: a chain fires only against instances of the work
document that declared it.

## Surface

| Field | Meaning |
|---|---|
| `id` | Identifies the chain within the declaring document. |
| `workflow` | The workflow to run. A static reference. |
| `placement` | Where the spawned session sits relative to this one. |
| `when` | The facts that must hold for the chain to fire. |
| `inputs` | The session inputs handed to the spawned workflow. |

<!-- fixture: chains/static-workflow.md -->
```markdown
+++
[pursue_goal]
kind              = "work"
description       = "Pursue one goal until an independent reviewer confirms it"
resource_observer = "goal"

[pursue_goal.done_when]
all = [
  { check = "resource.status.checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

[[pursue_goal.chains]]
id        = "goal_review"
workflow  = "goal_review_session"
placement = "sibling"

[pursue_goal.chains.when]
all = [
  { check = "resource.status.checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[pursue_goal.chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
+++
Pursue the goal at {{ resource.id }} until its checklist is satisfied.
```

## Triggers

`when.all` is a conjunction of facts, reading the same roots a completion leaf
does. A check fact compares one key and an expression fact states a computed
predicate, both exactly as in `done_when`. `judge_pending` holds while a named
judge leaf is still awaiting a verdict — which is how a chain spawns the
reviewer that will supply it. `judge_action` with `is` matches a recorded
verdict.

## Inputs

Chain inputs project the facts of the instance that fired — which session and
instance it was, which workflow, its pending judge ids — and the live roots it
reads, `resource.status.*` and `state.*`.

They read public facts only. A task layer's private locals do not cross into a
spawned session.

## Static workflow references

`workflow` is a static reference. Templated or otherwise computed workflow
selection is not part of the language: a chain's target is topology, and finite
dynamic selection would be its own language decision with its own concrete
consumer.

## Validation rules

- `workflow` resolves to a definition of kind `workflow`.
- `workflow` is never a computed value.
- A `when` judge id names a judge leaf this document's `done_when` declares.
- A check fact names a key the declared observer publishes, or one this
  document's `state_schema` declares.
- Chain inputs project public work facts, not locals.
- Chain inputs satisfy the target workflow's `inputs_schema`.
