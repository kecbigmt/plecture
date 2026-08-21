<!-- plect-fixture: result=valid entry=task -->
<!-- reason: acceptance check — a task document reads the observer's published keys directly, with no intermediate re-listing of them. -->
+++
[review]
kind              = "task"
description       = "Review a pull request against the keys its observer publishes"
resource_observer = "issue_pr"

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { check = "resource.state.checks_status", in = ["SUCCESS", "NULL"] },
]
+++
Review the pull request at {{ resource.id }}.
