<!-- plect-fixture: result=valid entry=work -->
<!-- reason: observe is the only surface with live roots, so a comparison against recorded state is evaluated afresh each time. -->
+++
kind        = "work"
description = "Review a pull request and record a verdict"
requires    = ["resource_kind", "verdict_current"]

[inputs]
instruction = { type = "string" }

# verdict_revision is written by the reviewer through `plect state
# set-output`, not observed from the resource, so it is declared rather than
# projected.
[records]
verdict_revision = { type = "string" }

[observe]
resource_kind   = { from = "resource.status.resource_kind" }
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
Review the pull request at {{ resource.id }} and record your verdict.
