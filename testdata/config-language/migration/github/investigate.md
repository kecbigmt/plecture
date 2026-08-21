<!-- plect-fixture: result=valid entry=work -->
<!-- reason: translation of plugins/github/config/tasks/investigate.toml plus plugins/github/config/templates/investigate.md into one work document. -->
+++
[investigate]
kind        = "work"
description = "Investigate an issue and summarize feasible approaches"
requires    = ["resource_kind", "checks_status", "issue_status"]

[investigate.inputs_schema]
type = "object"

[investigate.inputs_schema.properties]
instruction = { type = "string" }

# Resource status is kept as several observed keys rather than one rolled-up
# completion flag: collapsing a failing check and a still-running one onto a
# single value erases the failure-versus-pending distinction the check needs.
# A status that does not apply to this resource kind is the literal sentinel
# "NULL", which `in` accepts without it ever satisfying on its own.
[investigate.observe]
resource_kind   = { from = "resource.status.resource_kind" }
checks_status   = { from = "resource.status.checks_status" }
issue_status    = { from = "resource.status.issue_status" }
revision        = { from = "resource.status.revision" }
pr_url          = { from = "resource.status.pr_url", optional = true }
mergeable_state = { from = "resource.status.mergeable_state", optional = true }

[investigate.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[investigate.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Investigate the issue at {{ resource.id }}.

Steps:

1. Understand the resource (parse `<owner>/<repo>` and `<number>` from the URL
   {{ resource.id }}, then `gh api repos/<owner>/<repo>/issues/<number>`)
2. Investigate the relevant code
3. Decide on an approach
4. Carry it out
5. Verify it (tests, linters)
6. Report the outcome

Read via `gh api` (REST); reserve `gh issue`/`gh pr` porcelain for writes
(comments, reviews) — porcelain reads consume GraphQL quota that write-side
`gh` calls also share.

**Unattended session:** this session cannot ask the user interactive
questions. When a decision is needed, write it as a question in an issue or
PR comment and leave it as an unmet `done_when` criterion so it surfaces as
an escalation — don't proceed on an assumed answer.
{{- if inputs.instruction }}

Additional instructions: {{ inputs.instruction }}
{{- end }}
