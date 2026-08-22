+++
# Written for the issue observer: this is work on an issue, and the facts its
# completion reads — the linked pull request's check rollup, the issue's own
# status — are an issue observation's. A pull request keyed session responds
# to review comments instead (respond).
[work]
kind              = "task"
description       = "Implement a fix or feature for an issue and create a PR"
resource_observer = "issue"

[work.inputs_schema]
type                 = "object"
additionalProperties = false

[work.inputs_schema.properties]
instruction = { type = "string" }

# checks_status accepts the "NULL" sentinel alongside SUCCESS because a
# completion leaf can express neither a null literal nor OR: a head commit
# with no checks configured, or an issue with no linked pull request yet, has
# nothing to report, and `in [..., "NULL"]` accepts that without it ever
# satisfying on its own. A gh failure exits non-zero so the whole observation
# fails and prior values stand, never silently flipping a check to "NULL".
#
# mergeable_state is deliberately not read here: a merge conflict is a work
# signal, not a failure, so it must not gate completion.
[work.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["issue"] },
  { check = "resource.state.checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[work.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Resolve the issue at {{ resource.id }}.

Steps:

1. Understand the issue (parse `<owner>/<repo>` and `<number>` from the URL
   {{ resource.id }}, then `gh api repos/<owner>/<repo>/issues/<number>`)
2. Investigate the relevant code
3. Decide on an implementation approach
4. Implement the changes
5. Write and run tests
6. Commit and push
7. Create a PR (`gh pr create`)

Read via `gh api` (REST); reserve `gh issue`/`gh pr` porcelain for writes
(the PR itself, comments) — porcelain reads consume GraphQL quota that
write-side `gh` calls also share.

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
{{- if get .Inputs "instruction" ""}}

Additional instructions: {{get .Inputs "instruction" ""}}
{{- end}}
