<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a value reading a live root is current as of each evaluation, so no separate dynamic-output form exists. -->
+++
[review]
kind              = "work"
description       = "Review a resource and record a verdict against its revision"
resource_observer = "issue_pr"

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.status.resource_kind", in = ["pull", "issue"] },
  { expr = "state.verdict_revision == resource.status.revision" },
]
+++
Review {{ resource.id }} and record a verdict against its current revision.
