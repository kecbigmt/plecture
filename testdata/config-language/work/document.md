<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a work document's frontmatter is its completion contract and its body is the instruction. -->
+++
[work]
kind        = "work"
description = "Implement a fix or feature for an issue and create a PR"
requires    = ["resource_kind", "checks_status", "issue_status"]

[work.inputs_schema]
type = "object"

[work.inputs_schema.properties]
instruction = { type = "string" }

[work.observe]
resource_kind = { from = "resource.status.resource_kind" }
checks_status = { from = "resource.status.checks_status" }
issue_status  = { from = "resource.status.issue_status" }
revision      = { from = "resource.status.revision" }
pr_url        = { from = "resource.status.pr_url", optional = true }

[work.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[work.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Resolve the issue at {{ resource.id }}.

Steps:

1. Understand the issue
2. Investigate the relevant code
3. Implement the changes
4. Write and run tests
5. Commit and push, then open a pull request
