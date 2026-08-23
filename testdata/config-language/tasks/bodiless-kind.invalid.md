<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-BODILESS-IN-TASK-DOCUMENT entry=task -->
<!-- reason: an effect has no body, so declaring one in frontmatter would leave the instruction below with nothing that reads it. -->
+++
[runtime]
kind  = "effect"
scope = "run"

[runtime.setup]
type   = "shell"
script = "echo up"
+++
Launch the agent.
