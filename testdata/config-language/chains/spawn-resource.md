<!-- plect-fixture: result=valid entry=task -->
<!-- reason: a chain names the resource its spawned session binds to, projected from the facts the firing instance reads. -->
+++
[work]
kind              = "task"
description       = "Implement a fix and hand the pull request to a reviewer"
resource_observer = "issue_pr"

[work.done_when]
all = [
  { check = "resource.state.checks_status", in = ["SUCCESS"] },
  { judge = "acceptance criteria are satisfied", id = "ac-met" },
]

[[work.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"
resource  = { from = "resource.state.pr_url" }

[work.chains.when]
all = [
  { check = "resource.state.checks_status", in = ["SUCCESS"] },
  { judge_pending = "ac-met" },
]

[work.chains.inputs]
task         = "goal_review"
work_session = { from = "task.session" }
judge_ids    = { from = "task.done_when.pending_judge_ids" }
+++
Resolve the issue at {{ resource.id }} and open a pull request.
