<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-FIELD-UNKNOWN entry=work -->
<!-- reason: lifecycle belongs to a task; a work document brings nothing up and takes nothing down. -->
+++
[broken_work]
kind              = "work"
description       = "A work document that tries to own a lifecycle"
resource_observer = "issue_pr"

[broken_work.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[broken_work.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
