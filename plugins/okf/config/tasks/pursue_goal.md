+++
# pursue_goal: one instance tracks one local-okf goal Concept. Add it to the
# owner's orchestrator session with the goal's resource id; the instance name
# convention is goal_<slug> (hyphens replaced with underscores, since instance
# names must be Go-template identifiers) so a second setup of the same goal
# collides on --name instead of silently duplicating:
#
#   plect task setup pursue_goal \
#     --session <owner>/_orchestrator \
#     --name goal_<slug> \
#     --resource local-okf://<owner>/goals/<slug>.md
#
# Goal-specific completion conditions live in the goal file's "## Done When"
# checklist, which the observer reports as checklist_status — not in generated
# done_when config.
#
# This document declares no [[chains]], although a goal's reviewer is spawned
# by one. A chain's `workflow` is a static reference, and a reference written
# inside a plugin resolves only in that plugin's own namespace; okf ships no
# runnable reviewer workflow, because which agent runs a review is a choice
# the OKF specification does not make. A host that wants the reviewer spawned
# automatically declares its own pursue_goal document carrying the chain —
# docs/migrations/shipped-completion-conversion.md gives it verbatim.
[pursue_goal]
kind              = "task"
description       = "Pursue one goal until an independent reviewer confirms it"
resource_observer = "okf_goal"

[pursue_goal.inputs_schema]
type                 = "object"
additionalProperties = false

[pursue_goal.inputs_schema.properties]
instruction = { type = "string" }

# goal_parse_status must be SUCCESS: FAILURE (malformed file) and UNRESOLVED
# (goal not locatable — e.g. orchestrator destroy in progress) both keep the
# check unsatisfied. goal_status must still be "open": completed is the
# consequence of satisfaction, never its precondition. The goal usually lives
# on the session-tree root (the owner's orchestrator), which has no parent and
# supervises its own children — so child is deliberately excluded (a
# supervised child approving its own supervisor's goal is a back door, not
# independence). sibling alone is accepted.
[pursue_goal.done_when]
all = [
  { check = "resource.state.goal_parse_status", in = ["SUCCESS"] },
  { check = "resource.state.goal_status", in = ["open"] },
  { check = "resource.state.checklist_status", in = ["SUCCESS"] },
  { judge = "goal is achieved according to the goal file and event evidence", id = "goal-met", relation = ["sibling"] },
]

# No budget: heartbeat_budget is a convergence bound, and a long-lived goal
# existing is not exhaustion — unbounded by omission.
+++
Pursue the goal at {{ resource.id }} until its checklist is satisfied.

The goal file itself is the specification: its "## Done When" checklist says
what done means, and this instance reads the checklist's observed status
rather than restating it here.

Steps:

1. Read the goal file (symlinked into this session's workspace directory as
   `knowledge/`) — its checklist, its current status, and the log of what has
   happened against it so far
2. Work the open items, dispatching a session for any that needs one
3. Keep the checklist current as items land: it is what this instance observes
4. Once every item is checked, an independent reviewer judges whether the goal
   itself is achieved — answer its questions rather than recording a verdict
   of your own, which is structurally rejected
{{- if get .Inputs "instruction" ""}}

Additional instructions: {{get .Inputs "instruction" ""}}
{{- end}}
