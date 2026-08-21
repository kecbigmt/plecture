<!-- plect-fixture: result=valid entry=work -->
<!-- reason: acceptance check — the keys a work document reads from a resource observer are declared per key, with renames. -->
+++
[review]
kind        = "work"
description = "Review a pull request, reading the observer's state under this work's own names"
requires    = ["kind", "checks"]

[review.observe]
kind     = { from = "resource.status.resource_kind" }
checks   = { from = "resource.status.checks_status" }
revision = { from = "resource.status.revision" }
pr_url   = { from = "resource.status.pr_url", optional = true }

[review.done_when]
all = [
  { check = "kind", in = ["pull", "issue"] },
  { check = "checks", in = ["SUCCESS", "NULL"] },
]
+++
Review the pull request at {{ resource.id }}.
