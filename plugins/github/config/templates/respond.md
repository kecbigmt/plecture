---
description: Address review comments on a PR and push fixes
---
Address the review comments on PR at {{.ResourceID}}.

Steps:

1. Understand the PR (parse `<owner>/<repo>` and `<number>` from the URL
   {{.ResourceID}}, then `gh api repos/<owner>/<repo>/pulls/<number>`)
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

To follow this PR's progress while you work, run `plect subscribe {{.ResourceID}}` — its CI / review / merge events then arrive in this session (`plect event list`).

**Unattended session:** this session cannot ask the user interactive
questions. When a decision is needed, write it as a question in an issue or
PR comment and leave it as an unmet `done_when` criterion so it surfaces as
an escalation — don't proceed on an assumed answer.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
