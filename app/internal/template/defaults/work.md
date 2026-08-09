---
description: Implement a fix or feature for an issue and create a PR
---
Resolve the issue {{.OwnerRepo}}#{{.Number}}.

URL: {{.URL}}

Steps:

1. Understand the issue (`gh issue view {{.Number}}`)
2. Move the issue to "In Progress" on the project board using kbn MCP tools: `kbn_list` (owner from {{.OwnerRepo}}) → `kbn_list_items` → find this issue → `kbn_update_item_field` (field_name: "Status", value: "In Progress")
3. Investigate the relevant code
4. Decide on an implementation approach
5. Implement the changes
6. Write and run tests
7. Commit and push
8. Create a PR (`gh pr create`)
9. Move the issue to "In Review" on the project board using `kbn_update_item_field` (field_name: "Status", value: "In Review")

To track a related PR (one you opened, or a dependency you're waiting on), run `tws subscribe <pr-url>` — its CI / review / merge events then arrive in this session (`tws event list`).
To ask another session to act (e.g. request a re-review after pushing fixes), publish an event to it (`tws event publish <target-session> --type user.emit ...`) — a GitHub PR comment is a public mirror, not a delivery channel, and may never reach it.

**Note for unattended sessions:** this session cannot ask the user interactive
questions. If a judgment call is needed, write the question as an issue/PR
comment and surface it as an unmet done_when condition to escalate — don't
just assume and proceed.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
