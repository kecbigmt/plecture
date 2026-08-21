<!-- plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-FROM-ROOT entry=work -->
<!-- reason: observe reads the resource and this work's own recorded state; a node output is workflow wiring, not an observation. -->
+++
[broken_observe]
kind        = "work"
description = "A work document reaching for a node output"
requires    = ["session_name"]

[broken_observe.observe]
session_name = { from = "nodes.pane.outputs.session_name" }

[broken_observe.done_when]
all = [{ check = "session_name", ne = "" }]
+++
Resolve the issue at {{ resource.id }}.
