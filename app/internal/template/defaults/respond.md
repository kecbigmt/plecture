---
description: Address review comments on a PR and push fixes
---
Address the review comments on PR {{.Workflow.outputs.owner_repo}}#{{.Workflow.outputs.number}}.

URL: {{.ResourceID}}

Steps:

1. Understand the PR (`gh pr view {{.Workflow.outputs.number}}`)
2. Read the review comments (`gh pr view {{.Workflow.outputs.number}} --comments`)
3. Understand each comment's feedback
4. Implement the requested changes
5. Verify the fixes (run tests, linters, etc.)
6. Commit and push
7. Summarize what was addressed

To follow this PR's progress while you work, run `sennit subscribe {{.ResourceID}}` — its CI / review / merge events then arrive in this session (`sennit event list`).

**Note for unattended sessions:** this session cannot ask the user interactive
questions. If a judgment call is needed, write the question as an issue/PR
comment and surface it as an unmet done_when condition to escalate — don't
just assume and proceed.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
