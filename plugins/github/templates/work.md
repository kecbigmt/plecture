---
description: Implement a fix or feature for an issue and create a PR
---
Resolve the issue at {{.ResourceID}}.

Steps:

1. Understand the issue (`gh issue view {{.ResourceID}}`)
2. Investigate the relevant code
3. Decide on an implementation approach
4. Implement the changes
5. Write and run tests
6. Commit and push
7. Create a PR (`gh pr create`)

To track a related PR (one you opened, or a dependency you're waiting on), run
`plect subscribe <pr-url>` — its CI / review / merge events then arrive in
this session (`plect event list`).
To ask another session to act (e.g. request a re-review after pushing
fixes), publish an event to it (`plect event publish <target-session>
--type user.emit ...`) — a GitHub PR comment is a public mirror, not a
delivery channel, and may never reach it.

**Unattended session:** this session cannot ask the user interactive
questions. When a decision is needed, write it as a question in an issue or
PR comment and leave it as an unmet `done_when` criterion so it surfaces as
an escalation — don't proceed on an assumed answer.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
