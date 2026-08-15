---
description: Investigate an issue and summarize feasible approaches
---
Investigate the issue at {{.ResourceID}}.

Steps:

1. Understand the issue (parse `<owner>/<repo>` and `<number>` from the URL
   {{.ResourceID}}, then `gh api repos/<owner>/<repo>/issues/<number>`)
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
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
