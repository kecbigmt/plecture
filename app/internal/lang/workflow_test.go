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
resource = "issue"

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

func TestWorkflowNodes_SubscriptionReadsOnlyOwnOutput(t *testing.T) {
	valid := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"
[[session.nodes]]
uses = "thread"
subscription = { from = "self.outputs.permalink" }
`)
	if err := (Validation{}).ValidateDefinition(valid); err != nil {
		t.Fatalf("valid subscription: %v", err)
	}
	for _, declaration := range []string{
		`subscription = { from = "nodes.other.outputs.permalink" }`,
		`subscription = { from = "self.outputs.permalink", default = "fallback" }`,
		`subscription = { expr = "self.outputs.permalink" }`,
		`subscription = "self.outputs.permalink"`,
		`subscription = { from = "self.outputs.permalink", inputs = {} }`,
	} {
		t.Run(declaration, func(t *testing.T) {
			def := workflowDef(t, "[session]\nkind = \"workflow\"\nresource = \"issue\"\n[[session.nodes]]\nuses = \"thread\"\n"+declaration)
			if err := (Validation{}).ValidateDefinition(def); err == nil {
				t.Fatal("invalid subscription was accepted")
			}
		})
	}
}

func TestWorkflowNodes_QualifiedUsesDefaultsToReferencedID(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

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
resource = "issue"

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
resource = "issue"

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
resource = "issue"

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
		def := workflowDef(t, "[session]\nkind = \"workflow\"\nresource = \"issue\"\n\n"+body)
		assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeFieldUnknown, LayerStructural)
	}
}

func TestValidateWorkflow_CycleThroughBlocksRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

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

// An edge to an id this definition does not declare is the compiled plan's
// question, not this pass's.
func TestValidateWorkflow_EdgeToUndeclaredNodeIsNotACycle(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[[session.nodes]]
uses = "reaper"

[session.nodes.inputs]
peer = { from = "nodes.pane.outputs.session_name" }
`)
	if err := (Validation{}).ValidateDefinition(def); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
}

// A value surface accepts a whole JSON document built from projections, so an
// edge can be declared arbitrarily deep inside one. Validation already walks
// those leaves; the topology has to walk the same ones or the graph it reports
// is not the graph the wiring declares.
func TestWorkflowNodes_ReadsThroughAJSONOperand(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[[session.nodes]]
uses = "producer"

[[session.nodes]]
uses = "consumer"

[session.nodes.inputs]
payload = { json = { nested = { list = [ { from = "nodes.producer.outputs.value" } ] } } }
`)
	nodes, err := WorkflowNodes(def)
	if err != nil {
		t.Fatalf("WorkflowNodes: %v", err)
	}
	if len(nodes[1].Reads) != 1 || nodes[1].Reads[0] != "producer" {
		t.Fatalf("consumer reads = %v, want [producer]", nodes[1].Reads)
	}
}

func TestValidateWorkflow_CycleThroughAJSONOperandRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[[session.nodes]]
uses = "first"

[session.nodes.inputs]
payload = { json = { peer = { from = "nodes.second.outputs.value" } } }

[[session.nodes]]
uses = "second"

[session.nodes.inputs]
peer = { from = "nodes.first.outputs.value" }
`)
	assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeWorkflowCycle, LayerSemantic)
}

// A channel binding that is not an array of tables reaches the runtime as no
// channels at all, so the session would sit unreachable with nothing said.
func TestValidateWorkflow_NonArrayEventChannelRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[session.event]
channel = "runtime"

[[session.nodes]]
uses = "pane"
`)
	assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeFieldType, LayerStructural)
}

func TestValidateWorkflow_EventChannelRequiresNameAndUses(t *testing.T) {
	for _, body := range []string{
		"[[session.event.channel]]\nuses = \"delivery\"\n",
		"[[session.event.channel]]\nname = \"runtime\"\n",
	} {
		def := workflowDef(t, "[session]\nkind = \"workflow\"\nresource = \"issue\"\n\n[[session.nodes]]\nuses = \"pane\"\n\n"+body)
		assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeFieldRequired, LayerStructural)
	}
}

func TestValidateWorkflow_PublicOutputsAcceptedOverNodeOutputsAndSessionInputs(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[session.outputs]
endpoint = { from = "nodes.worker.outputs.endpoint" }
label    = { expr = "'run-' + session.inputs.label" }

[session.outputs_schema]
type = "object"

[[session.nodes]]
uses = "worker"
`)
	if err := (Validation{}).ValidateDefinition(def); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
}

func TestValidateWorkflow_PublicOutputRejectsRootsOutsideItsSurface(t *testing.T) {
	for _, binding := range []string{
		`{ from = "workflow.outputs.other" }`,
		`{ from = "self.outputs.pid" }`,
		`{ expr = "workflow.outputs.other + 'x'" }`,
	} {
		def := workflowDef(t, "[session]\nkind = \"workflow\"\nresource = \"issue\"\n\n[session.outputs]\nendpoint = "+binding+"\n")
		if err := (Validation{}).ValidateDefinition(def); err == nil {
			t.Fatalf("binding %s accepted, want a diagnostic", binding)
		}
	}
}

func TestWorkflowNodes_ReadOfPublicOutputExpandsToItsProducers(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[session.outputs]
endpoint = { from = "nodes.orchestrator.outputs.endpoint" }
combined = { expr = "nodes.agent.outputs.a + nodes.checkout.outputs.b" }

[[session.nodes]]
uses = "checkout"

[[session.nodes]]
uses = "agent"

[[session.nodes]]
uses = "orchestrator"

[[session.nodes]]
uses = "consumer"

[session.nodes.inputs]
peer = { from = "workflow.outputs.endpoint" }
both = { json = { x = { from = "workflow.outputs.combined" } } }
`)
	nodes, err := WorkflowNodes(def)
	if err != nil {
		t.Fatalf("WorkflowNodes: %v", err)
	}
	got := nodes[3].Reads
	want := []string{"orchestrator", "agent", "checkout"}
	if len(got) != len(want) {
		t.Fatalf("consumer reads = %v, want %v", got, want)
	}
	for _, id := range want {
		found := false
		for _, read := range got {
			found = found || read == id
		}
		if !found {
			t.Fatalf("consumer reads = %v, want it to include %q", got, id)
		}
	}
}

func TestValidateWorkflow_CycleThroughPublicOutputRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[session.outputs]
endpoint = { from = "nodes.agent.outputs.endpoint" }

[[session.nodes]]
uses = "agent"

[session.nodes.inputs]
peer = { from = "workflow.outputs.endpoint" }
`)
	assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeWorkflowCycle, LayerSemantic)
}

func TestValidateWorkflow_CycleAcrossNodesThroughPublicOutputRejected(t *testing.T) {
	def := workflowDef(t, `
[session]
kind = "workflow"
resource = "issue"

[session.outputs]
from_second = { from = "nodes.second.outputs.value" }

[[session.nodes]]
uses = "first"

[session.nodes.inputs]
peer = { from = "workflow.outputs.from_second" }

[[session.nodes]]
uses = "second"

[session.nodes.inputs]
peer = { from = "nodes.first.outputs.value" }
`)
	assertDiagnostic(t, Validation{}.ValidateDefinition(def), CodeWorkflowCycle, LayerSemantic)
}
