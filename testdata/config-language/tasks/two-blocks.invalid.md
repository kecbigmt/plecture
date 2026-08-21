<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-TASK-BLOCK-COUNT entry=task -->
<!-- reason: the body belongs to one declaration, so a frontmatter holding two has no way to say which. -->
+++
[work]
kind              = "task"
description       = "One of two declarations competing for the same body"
resource_observer = "issue_pr"

[work.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]

[review]
kind              = "task"
description       = "The other one"
resource_observer = "issue_pr"

[review.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
