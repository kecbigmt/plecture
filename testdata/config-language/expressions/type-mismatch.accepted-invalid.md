<!-- plect-fixture: result=accepted-invalid layer=cel diagnostic=PLECT-CFG-CEL-TYPE entry=work -->
<!-- reason: JSON Schema to CEL type projection is the sanctioned first check to drop, so this loads while result-type checking is disabled. -->
+++
kind        = "work"
description = "A work document whose computed observation does not type-check"
requires    = ["verdict_current"]

[records]
verdict_revision = { type = "string" }

[observe]
verdict_current = { expr = "self.verdict_revision + 1" }

[done_when]
all = [{ check = "verdict_current", in = [true] }]
+++
Review {{ resource.id }} and record a verdict.
