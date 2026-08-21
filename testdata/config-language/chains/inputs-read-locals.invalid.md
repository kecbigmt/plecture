<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECT-CFG-FROM-ROOT entry=work -->
<!-- reason: a chain passes on public work facts; a task layer's private locals do not cross into a spawned session. -->
+++
[pursue_goal]
kind        = "work"
description = "A work document leaking a private local into its chain"

[[pursue_goal.chains]]
id        = "review"
workflow  = "goal_review_session"
placement = "sibling"

[pursue_goal.chains.when]
all = [{ judge_pending = "goal-met" }]

[pursue_goal.chains.inputs]
secret = { from = "locals.guard_dir" }
+++
Pursue the goal at {{ resource.id }}.
