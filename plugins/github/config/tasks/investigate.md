+++
# Written for the issue observer, like work: an investigation's subject is the
# issue. See tasks/work.md for why checks_status accepts the "NULL" sentinel.
[investigate]
kind              = "task"
description       = "Investigate an issue and summarize feasible approaches"
resource_observer = "issue"

[investigate.inputs_schema]
type                 = "object"
additionalProperties = false

[investigate.inputs_schema.properties]
instruction = { type = "string" }

[investigate.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["issue"] },
  { check = "resource.state.checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[investigate.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Investigate the issue at {{ resource.id }}.

Steps:

1. Understand the issue (parse `<owner>/<repo>` and `<number>` from the URL
   {{ resource.id }}, then `gh api repos/<owner>/<repo>/issues/<number>`)
2. Research the relevant code and design
3. Identify the scope of impact
4. List feasible approaches
5. Evaluate pros and cons of each approach
6. Summarize the recommended approach with rationale
7. Post the findings as a comment on the issue

Read via `gh api` (REST); reserve `gh issue`/`gh pr` porcelain for writes
(comments, reviews) — porcelain reads consume GraphQL quota that write-side
`gh` calls also share.

**AI disclosure (mandatory):** every comment you post to GitHub must open by
identifying you as an AI agent. Start each body with:
`> 🤖 This comment was written by an AI agent (Claude Code) under human supervision.`

**Unattended session:** this session cannot ask the user interactive
questions. When a decision is needed, write it as a question in an issue or
PR comment and leave it as an unmet `done_when` criterion so it surfaces as
an escalation — don't proceed on an assumed answer.
{{- if get .Inputs "instruction" ""}}

Additional instructions: {{get .Inputs "instruction" ""}}
{{- end}}
