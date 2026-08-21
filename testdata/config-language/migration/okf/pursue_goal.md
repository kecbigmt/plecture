<!-- plect-fixture: result=valid entry=work -->
<!-- reason: translation of plugins/okf/config/tasks/pursue_goal.toml; it is the one work-genre declaration with no instruction template, so its body is written here. -->
+++
kind        = "work"
description = "Track one local-okf goal Concept until an independent reviewer confirms it"
requires    = ["goal_parse_status", "goal_status", "checklist_status"]

# Observed goal state is projected per key from the goal observer, so
# `plect resource status`, show, and this instance all read one contract.
# revision (= goal_revision) is the key judge staleness compares, so editing
# the goal file re-pends a recorded judge.
[observe]
goal_parse_status = { from = "resource.status.goal_parse_status" }
goal_status       = { from = "resource.status.goal_status" }
checklist_status  = { from = "resource.status.checklist_status" }
goal_revision     = { from = "resource.status.goal_revision" }
revision          = { from = "resource.status.revision" }
open_items        = { from = "resource.status.open_items" }

# goal_parse_status must be SUCCESS: FAILURE (malformed file) and UNRESOLVED
# (goal not locatable) both keep the check unsatisfied — which is also what
# replaces the shipped setup gate, since a resource that cannot be parsed as a
# goal can never satisfy this check. goal_status must still be "open":
# completed is the consequence of satisfaction, never its precondition. The
# goal usually lives on the session-tree root, which has no parent and
# supervises its own children, so child is deliberately excluded.
[done_when]
all = [
  { check = "goal_parse_status", in = ["SUCCESS"] },
  { check = "goal_status", in = ["open"] },
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

# No budget: heartbeat_budget is a convergence bound, and a long-lived goal
# existing is not exhaustion — unbounded by omission.

# Once the checklist is fully satisfied and only goal-met is pending, spawn an
# independent reviewer as this session's sibling. The chain instance is keyed
# by this instance, so each goal tracked on the same session gets its own
# reviewer instead of colliding on one chain-spawn tag.
[[chains]]
id        = "goal_review"
workflow  = "goal_review_session"
placement = "sibling"

[chains.when]
all = [
  { check = "goal_parse_status", in = ["SUCCESS"] },
  { check = "goal_status", in = ["open"] },
  { check = "checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[chains.inputs]
task         = "goal_review"
work_session = { from = "work.session" }
instance     = { from = "work.instance" }
judge_ids    = { from = "work.done_when.pending_judge_ids" }
+++
Pursue the goal at {{ resource.id }}.

Work its "## Done When" checklist to completion, recording evidence as you go.
Goal-specific completion conditions live in the goal file's checklist, not
here.
