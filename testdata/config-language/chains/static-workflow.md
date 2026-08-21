<!-- plect-fixture: result=valid entry=task -->
<!-- reason: a chain names its workflow with a static reference and projects the work facts it passes on. -->
+++
[pursue_goal]
kind              = "task"
description       = "Pursue one goal until an independent reviewer confirms it"
resource_observer = "goal"

[pursue_goal.done_when]
all = [
  { check = "resource.state.checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

[[pursue_goal.chains]]
id        = "goal_review"
workflow  = "goal_review_session"
placement = "sibling"

[pursue_goal.chains.when]
all = [
  { check = "resource.state.checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[pursue_goal.chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
+++
Pursue the goal at {{ resource.id }} until its checklist is satisfied.
