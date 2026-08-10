---
description: Investigate an issue and summarize feasible approaches
---
Investigate the issue {{.OwnerRepo}}#{{.Number}}.

URL: {{.URL}}

Steps:

1. Understand the issue (`gh issue view {{.Number}}`)
2. Research the relevant code and design
3. Identify the scope of impact
4. List feasible approaches
5. Evaluate pros and cons of each approach
6. Summarize the recommended approach with rationale
7. Post the findings as a comment on the issue

**Note for unattended sessions:** this session cannot ask the user interactive
questions. If a judgment call is needed, write the question as an issue/PR
comment and surface it as an unmet done_when condition to escalate — don't
just assume and proceed.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
