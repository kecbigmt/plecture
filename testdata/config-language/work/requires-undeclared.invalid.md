<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECT-CFG-REQUIRES-UNDECLARED entry=work -->
<!-- reason: every done_when check names a required key, and every required key is observed or recorded. -->
+++
kind        = "work"
description = "A work document whose check reads nothing it observes"
requires    = ["checks_status"]

[observe]
checks_status = { from = "resource.status.checks_status" }

[done_when]
all = [{ check = "mergeable_state", in = ["clean"] }]
+++
Resolve the issue at {{ resource.id }}.
