<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECT-CFG-REF-DYNAMIC entry=work -->
<!-- reason: a chain's workflow reference is static, so templated selection is not part of the language. -->
+++
[pursue_goal]
kind        = "work"
description = "A work document choosing its reviewer workflow at run time"

[[pursue_goal.chains]]
id        = "review"
workflow  = { from = "inputs.review_workflow" }
placement = "sibling"

[pursue_goal.chains.when]
all = [{ judge_pending = "goal-met" }]
+++
Pursue the goal at {{ resource.id }}.
