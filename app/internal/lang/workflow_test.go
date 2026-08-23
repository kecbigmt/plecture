package lang

import "testing"

func workflowDef(t *testing.T, body string) *Definition {
	t.Helper()
	defs, err := ParseDefinitionDocument("workflows/session.toml", []byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("parsed %d definitions, want 1", len(defs))
	}
	return defs[0]
}

func TestWorkflowNodes_DerivesIDsAndReads(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses = "pane"

[[session.nodes]]
id   = "worker"
uses = "official.codex.exec_runtime"

[session.nodes.inputs]
tmux_session = { from = "nodes.pane.outputs.session_name" }
label        = { expr = "'run-' + nodes.pane.outputs.session_name + session.name" }
literal      = "fixed"
`)
	nodes, err := WorkflowNodes(def)
	if err != nil {
		t.Fatalf("WorkflowNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	if nodes[0].ID != "pane" || nodes[0].Uses != "pane" {
		t.Fatalf("node 0 = %+v, want id/uses pane", nodes[0])
	}
	if nodes[1].ID != "worker" || nodes[1].Uses != "official.codex.exec_runtime" {
		t.Fatalf("node 1 = %+v", nodes[1])
	}
	if len(nodes[1].Reads) != 1 || nodes[1].Reads[0] != "pane" {
		t.Fatalf("node 1 reads = %v, want [pane]", nodes[1].Reads)
	}
}

func TestWorkflowNodes_QualifiedUsesDefaultsToReferencedID(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses = "official.tmux.pane"
`)
	nodes, err := WorkflowNodes(def)
	if err != nil {
		t.Fatalf("WorkflowNodes: %v", err)
	}
	if nodes[0].ID != "pane" {
		t.Fatalf("defaulted id = %q, want pane", nodes[0].ID)
	}
}

func TestWorkflowNodes_DuplicateIDRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses = "pane"

[[session.nodes]]
id   = "pane"
uses = "official.tmux.other"
`)
	_, err := WorkflowNodes(def)
	assertDiagnostic(t, err, CodeIDDuplicate, LayerSemantic)
}

func TestWorkflowNodes_NodeWithoutUsesRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
id = "worker"
`)
	_, err := WorkflowNodes(def)
	assertDiagnostic(t, err, CodeFieldRequired, LayerStructural)
}

func TestWorkflowNodes_UnknownNodeFieldRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses       = "pane"
depends_on = ["other"]
`)
	_, err := WorkflowNodes(def)
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
}

// A misspelled clock field is a declaration with no consumer: read as unset,
// it would leave the author believing the cadence is in force.
func TestValidateWorkflow_UnknownClockFieldRejected(t *testing.T) {
	for _, body := range []string{
		"[session.tick]\nstale_when = \"15m\"\n",
		"[session.healthcheck]\nperiod_seconds = 300\n",
	} {
		def := workflowDef(t, "[session]\nkind = \"workflow\"\n\n"+body)
		assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeFieldUnknown, LayerStructural)
	}
}

func TestValidateWorkflow_CycleThroughBlocksRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses   = "pane"
blocks = ["reaper"]

[session.nodes.inputs]
peer = { from = "nodes.reaper.outputs.pid" }

[[session.nodes]]
uses = "reaper"
`)
	err := Validation{}.ValidateDefinition(def)
	assertDiagnostic(t, err, CodeWorkflowCycle, LayerSemantic)
}

// A cascade layer may add a node whose inputs read a base layer's node, so an
// edge to an id this definition does not declare is the merged load's
// question, not this pass's.
func TestValidateWorkflow_EdgeToUndeclaredNodeIsNotACycle(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"

[[session.nodes]]
uses = "reaper"

[session.nodes.inputs]
peer = { from = "nodes.pane.outputs.session_name" }
`)
	if err := (Validation{}).ValidateDefinition(def); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
}
