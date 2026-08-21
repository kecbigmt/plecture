<!-- plect-fixture: result=invalid layer=instantiation diagnostic=PLECTURE-CFG-RESOURCE-OBSERVER-MISMATCH entry=work -->
<!-- reason: the document loads — every key it reads is published by the observer it declares — but binding an instance to a resource of another kind fails up front, rather than producing an instance that can never satisfy. -->
+++
[review]
kind              = "work"
description       = "Written for a pull request, instantiated against a goal file"
resource_observer = "issue_pr"

[review.done_when]
all = [
  { check = "resource.status.resource_kind", in = ["pull", "issue"] },
  { check = "resource.status.checks_status", in = ["SUCCESS", "NULL"] },
]
+++
Review the pull request at {{ resource.id }}.
