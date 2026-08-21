<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-FROM-PATH entry=work -->
<!-- reason: an observation names a key the resolved observer's state schema does not declare. -->
+++
[broken_observe]
kind        = "work"
description = "A work document misspelling an observed key"
requires    = ["checks"]

[broken_observe.observe]
checks = { from = "resource.status.check_status" }

[broken_observe.done_when]
all = [{ check = "checks", in = ["SUCCESS"] }]
+++
Review the pull request at {{ resource.id }}.
