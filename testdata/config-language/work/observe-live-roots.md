<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a completion leaf may compare two live roots, which is how a recorded verdict is invalidated by a later change without any key restating it. -->
+++
[review]
kind              = "work"
description       = "Review a pull request and record a verdict"
resource_observer = "issue_pr"

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

# verdict_revision is this work's own state: written into the instance by the
# reviewer rather than published by the observer. It carries no mutability
# annotation, because state is mutable by definition.
[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
]

[review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the pull request at {{ resource.id }} and record your verdict.
