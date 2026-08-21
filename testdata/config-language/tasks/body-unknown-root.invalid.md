<!-- plect-fixture: result=invalid layer=semantic diagnostic=PLECTURE-CFG-FROM-ROOT entry=task -->
<!-- reason: the body's projections are validated against the roots this surface declares, so a node output — workflow wiring — is not among them. -->
+++
[review]
kind              = "task"
description       = "A task document whose body reaches for workflow wiring"
resource_observer = "issue_pr"

[review.done_when]
all = [{ check = "resource.state.resource_kind", in = ["pull", "issue"] }]
+++
Review {{ resource.id }} in the pane {{ nodes.pane.outputs.session_name }}.
