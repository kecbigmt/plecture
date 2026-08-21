<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a chain names its workflow with a static reference and projects the work facts it passes on. -->
+++
kind        = "work"
description = "Pursue one goal until an independent reviewer confirms it"
requires    = ["checklist_status"]

[observe]
checklist_status = { from = "resource.status.checklist_status" }

[done_when]
all = [
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

[[chains]]
id        = "goal_review"
workflow  = "goal_review_session"
placement = "sibling"

[chains.when]
all = [
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
+++
Pursue the goal at {{ resource.id }} until its checklist is satisfied.
