+++
# Written for the pull observer: this task addresses review comments on a pull
# request, so its subject is that pull request rather than the issue it
# closes. See tasks/work.md for why checks_status accepts the "NULL" sentinel.
[respond]
kind              = "task"
description       = "Address review comments on a PR and push fixes"
resource_observer = "pull"

[respond.inputs_schema]
type                 = "object"
additionalProperties = false

[respond.inputs_schema.properties]
instruction = { type = "string" }

[respond.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull"] },
  { check = "resource.state.checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[respond.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Address the review comments on PR at {{ resource.id }}.

Steps:

1. Understand the PR (parse `<owner>/<repo>` and `<number>` from the URL
   {{ resource.id }}, then `gh api repos/<owner>/<repo>/pulls/<number>`)
2. Read the review comments (`gh api repos/<owner>/<repo>/pulls/<number>/comments`
   and `gh api repos/<owner>/<repo>/issues/<number>/comments`)
3. Understand each comment's feedback
4. Implement the requested changes
5. Verify the fixes (run tests, linters, etc.)
6. Commit and push
7. Summarize what was addressed

Read via `gh api` (REST); reserve `gh issue`/`gh pr` porcelain for writes
(comments, reviews) — porcelain reads consume GraphQL quota that write-side
`gh` calls also share.

**AI disclosure (mandatory):** every comment or reply you post to GitHub must
open by identifying you as an AI agent. Start each body with:
`> 🤖 This comment was written by an AI agent (Claude Code) under human supervision.`

To follow this PR's progress while you work, run `plect subscribe {{ resource.id }}` — its CI / review / merge events then arrive in this session (`plect event list`).

**Unattended session:** this session cannot ask the user interactive
questions. When a decision is needed, write it as a question in an issue or
PR comment and leave it as an unmet `done_when` criterion so it surfaces as
an escalation — don't proceed on an assumed answer.
{{- if get .Inputs "instruction" ""}}

Additional instructions: {{get .Inputs "instruction" ""}}
{{- end}}
