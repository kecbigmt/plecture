# Chains

A chain is a deterministic rule for spawning work off a task's instances: once
this instance reaches this state, run that workflow.

Chains are a task-owned construct, not a definition kind. They live in the
declaring task's block because a chain's `when` judge ids and `inputs` output
projections are references into that same task's completion contract and output
schema — colocation is what lets those references be checked at load time
rather than at fire time. Evaluation is scoped the same way: a chain fires only
against instances of the task that declared it.

## Surface

| Field | Meaning |
|---|---|
| `id` | Identifies the chain within the declaring task. |
| `workflow` | The workflow to run. A static reference. |
| `placement` | Where the spawned session sits relative to this one. |
| `when` | The facts that must hold for the chain to fire. |
| `inputs` | The session inputs handed to the spawned workflow. |

<!-- fixture: chains/static-workflow.toml -->
```toml
[pursue_goal]
kind     = "task"
scope    = "session"
requires = ["checklist_status"]

[pursue_goal.setup]
type = "exec"
bin  = "okf-goal"
args = ["task", "validate-goal-resource", "--resource", { from = "resource.id" }]

[pursue_goal.outputs.bind]
checklist_status = { from = "resource.status.checklist_status" }

[pursue_goal.outputs_schema]
type = "object"

[pursue_goal.outputs_schema.properties]
checklist_status = { type = "string", mutable = true }

[pursue_goal.done_when]
all = [
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

[[pursue_goal.chains]]
id        = "goal_review"
workflow  = "goal_review"
placement = "sibling"

[pursue_goal.chains.when]
all = [
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[pursue_goal.chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
```

## Triggers

`when.all` is a conjunction of facts. A check fact compares an observed output
the same way a completion check does. `judge_pending` holds while a named judge
leaf is still awaiting a verdict — which is how a chain spawns the reviewer
that will supply it. `judge_action` with `is` matches a recorded verdict.

## Inputs

Chain inputs project the work facts of the instance that fired: which session
and instance it was, which workflow, its public outputs, its pending judge
ids, and its revision.

They read public facts only. A layer's private locals do not cross into a
spawned session.

## Static workflow references

`workflow` is a static reference. Templated or otherwise computed workflow
selection is not part of the language: a chain's target is topology, and
finite dynamic selection would be its own language decision with its own
concrete consumer.

## Validation rules

- `workflow` resolves to a definition of kind `workflow`.
- `workflow` is never a computed value.
- A `when` judge id names a judge leaf this task's `done_when` declares.
- A check fact names an output this task declares.
- Chain inputs project public work facts, not locals.
- Chain inputs satisfy the target workflow's `inputs_schema`.
