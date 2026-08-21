<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-FROM-ROOT entry=work -->
<!-- reason: a completion key reads the observer's published state or this work's own; a node output is workflow wiring, not either. -->
+++
[broken_observe]
kind              = "work"
description       = "A work document reaching for a node output"
resource_observer = "issue_pr"

[broken_observe.done_when]
all = [{ check = "nodes.pane.outputs.session_name", ne = "" }]
+++
Resolve the issue at {{ resource.id }}.
