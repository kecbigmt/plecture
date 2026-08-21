<!-- plect-fixture: result=accepted-invalid layer=cel diagnostic=PLECT-CFG-CEL-TYPE entry=work -->
<!-- reason: JSON Schema to CEL type projection is the sanctioned first check to drop, so this loads while result-type checking is disabled. -->
+++
[review]
kind        = "work"
description = "A work document whose computed observation does not type-check"
requires    = ["verdict_current"]

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.observe]
verdict_current = { expr = "self.verdict_revision + 1" }

[review.done_when]
all = [{ check = "verdict_current", in = [true] }]
+++
Review {{ resource.id }} and record a verdict.
