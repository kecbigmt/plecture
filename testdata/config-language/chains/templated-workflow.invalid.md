<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-REF-DYNAMIC entry=task -->
<!-- reason: a chain's workflow reference is static, so templated selection is not part of the language. -->
+++
[pursue_goal]
kind              = "task"
description       = "A task document choosing its reviewer workflow at run time"
resource_observer = "goal"

[[pursue_goal.chains]]
id        = "review"
workflow  = { from = "inputs.review_workflow" }
placement = "sibling"

[pursue_goal.chains.when]
all = [{ judge_pending = "goal-met" }]
+++
Pursue the goal at {{ resource.id }}.
