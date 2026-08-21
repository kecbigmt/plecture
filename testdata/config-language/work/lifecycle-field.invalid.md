<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECT-CFG-FIELD-UNKNOWN entry=work -->
<!-- reason: lifecycle belongs to a task; a work document brings nothing up and takes nothing down. -->
+++
kind        = "work"
description = "A work document that tries to own a lifecycle"

[setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[done_when]
all = [{ check = "checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
