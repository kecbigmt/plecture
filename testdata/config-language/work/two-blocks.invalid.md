<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-WORK-BLOCK-COUNT entry=work -->
<!-- reason: the body belongs to one declaration, so a frontmatter holding two has no way to say which. -->
+++
[work]
kind        = "work"
description = "One of two declarations competing for the same body"

[work.done_when]
all = [{ check = "checks_status", in = ["SUCCESS"] }]

[review]
kind        = "work"
description = "The other one"

[review.done_when]
all = [{ check = "checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
