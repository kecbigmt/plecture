package lang

import "testing"

// TestSurfaceRootsMatchValuesTable walks the per-surface root table in
// docs/language/values.md: one accepted and one rejected projection per
// surface, so a row cannot be dropped or widened without a failure here.
func TestSurfaceRootsMatchValuesTable(t *testing.T) {
	tests := []struct {
		surface *Surface
		offered []string
		foreign []string
	}{
		{surfaceResourceName, []string{"match.owner", "match.repo"}, []string{"session.name", "inputs.owner"}},
		{surfaceResourceSubscribe, []string{"session.name", "resource.id", "resource.inputs.token"}, []string{"inputs.x", "workspace.dir", "session.branch"}},
		{surfaceResourceUnsubscribe, []string{"session.name", "resource.id", "resource.inputs.token"}, []string{"inputs.x", "workspace.dir", "session.branch"}},
		{surfaceResourceObserve, []string{"resource.id", "resource.inputs.token"}, []string{"resource.revision", "judges", "session.name", "workspace.dir", "workspace.branch"}},
		{surfaceResourceFinalize, []string{"resource.id", "session.name", "resource.revision", "judges", "resource.inputs.token"}, []string{"workspace.dir", "effect.instance", "inputs.x"}},
		{surfaceWorkflowDisplay, []string{"workflow.outputs.concept_id", "session.inputs.task"}, []string{"nodes.pane.outputs.session_name", "resource.id"}},
		{surfaceWorkflowNodeInputs, []string{"nodes.pane.outputs.session_name", "workflow.outputs.x", "session.name", "session.inputs.task"}, []string{"resource.id", "locals.x", "event.type", "workspace.dir"}},
		{surfaceChannelDelivery, []string{"event.type", "event.body", "event.metadata.url", "inputs.queue_dir"}, []string{"workflow.outputs.branch", "session.name"}},
		{surfaceChannelTimeout, []string{"inputs.enqueue_timeout"}, []string{"event.metadata.timeout", "session.name"}},
		{surfaceEffectSetup, []string{"inputs.owner", "prev.x", "nodes.pane.outputs.session_name", "workflow.outputs.x", "session.name", "session.inputs.owner", "resource.id"}, []string{"workspace.dir", "event.metadata.owner", "self.outputs.x", "locals.x", "resource.state.checks_status"}},
		{surfaceEffectCleanup, []string{"self.outputs.session_name", "self.process.identity", "inputs.x", "force", "nodes.pane.outputs.x", "workflow.outputs.x", "session.name"}, []string{"resource.id", "prev.x", "workspace.dir", "cleanup.inputs.delete_branch"}},
		{surfaceEffectHealth, []string{"self.outputs.session_name", "inputs.x", "session.name"}, []string{"nodes.pane.outputs.x", "resource.id", "self.process.identity", "workspace.dir"}},
		{surfaceEffectTerminal, []string{"self.outputs.session_name", "session.name"}, []string{"inputs.x", "workspace.dir"}},
		{surfaceEffectInner, []string{"inputs.tmux_session", "locals.guard_dir", "nodes.pane.outputs.session_name", "workflow.outputs.x", "session.name"}, []string{"inner.outputs.pid", "resource.id", "workspace.dir"}},
		{surfaceEffectOutputsBind, []string{"inner.outputs.pid", "locals.guard_dir", "inputs.mcp_servers"}, []string{"session.inputs.owner", "resource.state.checks_status", "inner.inputs.x"}},
		{surfaceTaskCompletion, []string{"resource.state.resource_kind", "self.state.verdict_revision"}, []string{"resource.id", "nodes.pane.outputs.session_name", "inputs.x"}},
		{surfaceTaskInstruction, []string{"resource.id", "resource.state.revision", "self.state.verdict_revision", "inputs.instruction", "session.name", "workflow.outputs.x"}, []string{"nodes.pane.outputs.session_name", "locals.x"}},
		{surfaceChainInputs, []string{"task.session", "task.instance", "task.workflow", "task.done_when.pending_judge_ids", "resource.state.checklist_status", "self.state.x"}, []string{"locals.guard_dir", "inputs.x"}},
	}
	for _, tc := range tests {
		t.Run(tc.surface.Name, func(t *testing.T) {
			for _, path := range tc.offered {
				if !tc.surface.offers(path) {
					t.Errorf("%s does not offer %q, but values.md lists it", tc.surface.Name, path)
				}
			}
			for _, path := range tc.foreign {
				if tc.surface.offers(path) {
					t.Errorf("%s offers %q, which values.md does not list", tc.surface.Name, path)
				}
			}
		})
	}
}

func TestSurfaceRootsAcceptAWholeRoot(t *testing.T) {
	if !surfaceChannelDelivery.offers("event") {
		t.Error("event.* covers the whole event, which a channel body serializes")
	}
	if !surfaceResourceFinalize.offers("judges") {
		t.Error("judges is a whole root on the finalize surface")
	}
	if surfaceResourceObserve.offers("session") {
		t.Error("the observe surface lists resource.id alone, not a session root")
	}
}

func TestSurfaceRootsAcceptANestedContractField(t *testing.T) {
	// Whether a contract declares the field is PLECTURE-CFG-FROM-PATH's
	// question, not this one's.
	if !surfaceEffectSetup.offers("inputs.mcp_servers.name") {
		t.Error("inputs.<key> covers a field nested inside the input contract")
	}
}

func TestSurfaceIdentifiers(t *testing.T) {
	got := surfaceTaskCompletion.identifiers()
	want := []string{"resource", "self"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
