<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-FROM-PATH entry=task -->
<!-- reason: a completion key names a key the declared observer does not publish, which the declaration makes a load error rather than a run-time surprise. -->
+++
[broken_key]
kind              = "task"
description       = "A task document reading a key its observer never publishes"
resource_observer = "issue_pr"

[broken_key.done_when]
all = [{ check = "resource.state.mergeability", in = ["clean"] }]
+++
Resolve the issue at {{ resource.id }}.
