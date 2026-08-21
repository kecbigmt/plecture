<!-- plect-fixture: result=invalid layer=instantiation diagnostic=PLECTURE-CFG-RESOURCE-MISMATCH entry=work -->
<!-- reason: the document loads — it declares a resource and observes keys that observer publishes — but binding an instance to a resource of another kind fails up front, rather than producing an instance that can never satisfy. -->
+++
[review]
kind        = "work"
description = "Written for a pull request, instantiated against a goal file"
resource    = "issue_pr"
requires    = ["resource_kind", "checks_status"]

[review.observe]
resource_kind = { from = "resource.status.resource_kind" }
checks_status = { from = "resource.status.checks_status" }

[review.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "checks_status", in = ["SUCCESS", "NULL"] },
]
+++
Review the pull request at {{ resource.id }}.
