<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-FROM-ROOT entry=task -->
<!-- reason: a chain passes on public work facts and observed state; an effect layer's private locals do not cross into a spawned session. -->
+++
[pursue_goal]
kind              = "task"
description       = "A task document leaking a private local into its chain"
resource_observer = "goal"

[[pursue_goal.chains]]
id        = "review"
workflow  = "goal_reviewer"
placement = "sibling"

[pursue_goal.chains.when]
all = [{ judge_pending = "goal-met" }]

[pursue_goal.chains.inputs]
secret = { from = "locals.guard_dir" }
+++
Pursue the goal at {{ resource.id }}.
