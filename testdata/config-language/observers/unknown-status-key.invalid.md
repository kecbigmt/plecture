<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECT-CFG-FROM-PATH entry=work -->
<!-- reason: an observation names a key the resolved observer's state schema does not declare. -->
+++
kind        = "work"
description = "A work document misspelling an observed key"
requires    = ["checks"]

[observe]
checks = { from = "resource.status.check_status" }

[done_when]
all = [{ check = "checks", in = ["SUCCESS"] }]
+++
Review the pull request at {{ resource.id }}.
