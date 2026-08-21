<!-- plect-fixture: result=valid entry=work -->
<!-- reason: translation of plugins/github/config/tasks/review.toml plus plugins/github/config/templates/review.md; the verdict_current script becomes a computation over live roots. -->
+++
kind        = "work"
description = "Review a PR and provide feedback as comments"
requires    = ["resource_kind", "verdict_current"]

[inputs]
instruction = { type = "string" }
pr_url      = { type = "string" }
work_session = { type = "string" }
judge_ids   = { type = "string" }

# There is no third party to judge a reviewer, so this document's done_when
# cannot use a judge leaf. Completion is the reviewer's own self-report of "I
# recorded a verdict", captured as a revision rather than a boolean so a later
# push invalidates it automatically. verdict_revision is written by the
# reviewer, not observed, so it is declared rather than projected.
[records]
verdict_revision = { type = "string" }

# Both operands read live roots, so the comparison is evaluated against the
# same observation rather than against a snapshot taken before this pass.
[observe]
resource_kind   = { from = "resource.status.resource_kind" }
checks_status   = { from = "resource.status.checks_status" }
issue_status    = { from = "resource.status.issue_status" }
revision        = { from = "resource.status.revision" }
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }

[done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "verdict_current", in = [true] },
]

[budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
{{- $target := inputs.pr_url | default resource.id -}}
Review the PR {{ $target }}.

Steps:

1. Understand the purpose and background of the PR — derive `<owner>/<repo>` and
   `<n>` from {{ $target }}'s URL and read
   `gh api repos/<owner>/<repo>/pulls/<n>` (REST; reserve `gh pr`/`gh issue`
   porcelain for writes, since porcelain reads consume GraphQL quota that
   write-side `gh` calls also share)
2. Review the diff (`gh api repos/<owner>/<repo>/pulls/<n> -H "Accept: application/vnd.github.diff"`)
3. Evaluate code quality, design, and security
4. Run relevant tests and linters
5. Identify risks, concerns, and open questions
6. Post the review as comments

**AI disclosure (mandatory):** every comment you post to GitHub must open by
identifying you as an AI agent.

Record your verdict by writing the revision you reviewed:

    plect state set-output <session> --instance <instance> verdict_revision=<sha>
{{- if inputs.instruction }}

Additional instructions: {{ inputs.instruction }}
{{- end }}
