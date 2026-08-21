<!-- plect-fixture: result=accepted-invalid layer=cel diagnostic=PLECTURE-CFG-CEL-TYPE entry=work -->
<!-- reason: JSON Schema to CEL type projection is the sanctioned first check to drop, so this loads while result-type checking is disabled. -->
+++
[review]
kind              = "work"
description       = "A work document whose computed leaf does not type-check"
resource_observer = "issue_pr"

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [{ expr = "self.verdict_revision + 1" }]
+++
Review {{ resource.id }} and record a verdict.
