---
description: Review a PR and provide feedback as comments
---
Review the PR {{.Workflow.outputs.owner_repo}}#{{.Workflow.outputs.number}}.

URL: {{.ResourceID}}

Steps:

1. Understand the purpose and background of the PR (`gh pr view {{.Workflow.outputs.number}}`)
2. Review the diff (`gh pr diff {{.Workflow.outputs.number}}`)
3. Evaluate code quality, design, and security
4. Run relevant tests and linters
5. Identify risks, concerns, and open questions
6. Prepare review comments
7. If review comments already exist, check their resolution status
8. When this review is for a plecture done_when judge, record one action per judge
   id with a reason:
   `plecture judge approve <work-session> <task-instance> <judge-id> --reason "<reason>"`
   Use `plecture judge request-changes <work-session> <task-instance> <judge-id> --reason "<reason>"`
   for any unmet criterion and name the missing work in the reason.
   If `plecture judge` rejects your verdict as self-review, the PR has no
   dispatched work session to record against (it was implemented directly) —
   record the verdict as a review comment with an explicit marker instead:
   `gh pr review {{.Workflow.outputs.number}} --comment --body "APPROVE: <reason>"` (or
   `"REQUEST_CHANGES: <reason>"`). A formal `--approve`/`--request-changes`
   is rejected by GitHub when your session shares the PR author's account.
   Sharing an account is a fact about GitHub transport identity, not about
   who the actor is — it does not make this review non-independent.

**Review perspective for changes touching plecture core:** if the change touches
plecture core (the Go implementation under `app/` etc.), ask whether a
prompt (template/instructions) or config (TOML etc.) could have achieved the
same result instead, and evaluate whether the added complexity is worth the
benefit. Reference this evaluation in the verdict's reason.

**Scope boundary (mandatory):** this review's responsibility ends at
recording the verdict. Do not perform write operations on the PR/issue
(merge / close / edit / apply labels, etc.). Even if both judges approve and
CI looks green, that alone is not grounds to merge — a human hold on the
decision lives outside judge state and may not be visible from this session.
Merging is the orchestrator's exclusive prerogative.

To follow this PR's progress while you work, run `plecture subscribe {{.ResourceID}}` — its CI / review / merge events then arrive in this session (`plecture event list`).

**Note for unattended sessions:** this session cannot ask the user interactive
questions. If a judgment call is needed, write the question as an issue/PR
comment and surface it as an unmet done_when condition to escalate — don't
just assume and proceed.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
