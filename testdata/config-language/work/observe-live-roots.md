<!-- plect-fixture: result=valid entry=work -->
<!-- reason: observe is the only surface with live roots, so a comparison against recorded state is evaluated afresh each time. -->
+++
[review]
kind        = "work"
description = "Review a pull request and record a verdict"
requires    = ["resource_kind", "verdict_current"]

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

# verdict_revision is this work's own state: written into the instance by the
# reviewer rather than observed from the resource. It carries no mutability
# annotation, because state is mutable by definition.
[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.observe]
resource_kind   = { from = "resource.status.resource_kind" }
revision        = { from = "resource.status.revision" }
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }

[review.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "verdict_current", in = [true] },
]

[review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the pull request at {{ resource.id }} and record your verdict.
