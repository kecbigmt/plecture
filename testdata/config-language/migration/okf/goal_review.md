<!-- plect-fixture: result=valid entry=work -->
<!-- reason: translation of plugins/okf/config/tasks/goal_review.toml plus plugins/okf/config/templates/goal_review.md. -->
+++
[goal_review]
kind        = "work"
description = "Review a local-okf goal file and record whether it is achieved"
requires    = ["goal_parse_status", "verdict_current"]

[goal_review.inputs_schema]
type = "object"

[goal_review.inputs_schema.properties]
instruction  = { type = "string" }
work_session = { type = "string" }
instance     = { type = "string" }
judge_ids    = { type = "string" }

[goal_review.state_schema]
type = "object"

[goal_review.state_schema.properties]
verdict_revision = { type = "string" }

[goal_review.observe]
goal_parse_status = { from = "resource.status.goal_parse_status" }
goal_status       = { from = "resource.status.goal_status" }
checklist_status  = { from = "resource.status.checklist_status" }
goal_revision     = { from = "resource.status.goal_revision" }
revision          = { from = "resource.status.revision" }
open_items        = { from = "resource.status.open_items" }
verdict_current   = { expr = "self.verdict_revision == resource.status.revision" }

[goal_review.done_when]
all = [
  { check = "goal_parse_status", in = ["SUCCESS"] },
  { check = "verdict_current", in = [true] },
]

[goal_review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the goal at {{ resource.id }}.

Steps:

1. Read the goal file's contents (symlinked into this session's workspace directory as
   `knowledge/`) — its "## Done When" checklist, its current status, and the
   log of what's happened against it so far
2. Cross-check the checklist against actual evidence: recent commits, PRs,
   issues, and events for the sessions that worked this goal — a checked box
   with no corresponding evidence is not achieved
3. Record your verdict on the target session's pending judge leaves
{{- if inputs.instruction }}

Additional instructions: {{ inputs.instruction }}
{{- end }}
