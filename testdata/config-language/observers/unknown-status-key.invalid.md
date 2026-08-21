<!-- plect-fixture: result=invalid layer=instantiation diagnostic=PLECTURE-CFG-FROM-PATH entry=work -->
<!-- reason: an observation names a key no observer publishes; the document loads, because which observer applies is unknown until a resource is bound, and instantiation is where this fails. -->
+++
[broken_observe]
kind        = "work"
description = "A work document misspelling an observed key"
requires    = ["checks"]

[broken_observe.observe]
checks = { from = "resource.status.check_status" }

[broken_observe.done_when]
all = [{ check = "checks", in = ["SUCCESS"] }]
+++
Review the pull request at {{ resource.id }}.
