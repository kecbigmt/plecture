<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a value reading a live root is current as of each evaluation, so no separate dynamic-output form exists. -->
+++
kind        = "work"
description = "Review a resource and record a verdict against its revision"
requires    = ["resource_kind", "verdict_current"]

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
+++
Review {{ resource.id }} and record a verdict against its current revision.
