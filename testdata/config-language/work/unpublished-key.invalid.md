<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-FROM-PATH entry=work -->
<!-- reason: a completion key names a key the declared observer does not publish, which the declaration makes a load error rather than a run-time surprise. -->
+++
[broken_requires]
kind              = "work"
description       = "A work document reading a key its observer never publishes"
resource_observer = "issue_pr"

[broken_requires.done_when]
all = [{ check = "resource.status.mergeability", in = ["clean"] }]
+++
Resolve the issue at {{ resource.id }}.
