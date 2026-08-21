<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-FIELD-UNKNOWN entry=task -->
<!-- reason: lifecycle belongs to an effect; a task document brings nothing up and takes nothing down. -->
+++
[broken_task]
kind              = "task"
description       = "A task document that tries to own a lifecycle"
resource_observer = "issue_pr"

[broken_task.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[broken_task.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
