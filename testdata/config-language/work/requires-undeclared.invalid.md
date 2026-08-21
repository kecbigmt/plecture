<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-REQUIRES-UNDECLARED entry=work -->
<!-- reason: every done_when check names a required key, and every required key is observed or recorded. -->
+++
[broken_requires]
kind        = "work"
description = "A work document whose check reads nothing it observes"
resource    = "issue_pr"
requires    = ["checks_status"]

[broken_requires.observe]
checks_status = { from = "resource.status.checks_status" }

[broken_requires.done_when]
all = [{ check = "mergeable_state", in = ["clean"] }]
+++
Resolve the issue at {{ resource.id }}.
