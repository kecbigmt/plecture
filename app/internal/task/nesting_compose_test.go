package task

import (
	"reflect"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func doneWhen(leaves ...config.DoneWhenLeaf) *config.DoneWhen {
	return &config.DoneWhen{All: leaves}
}

func checkLeaf(output, eq string) config.DoneWhenLeaf {
	return config.DoneWhenLeaf{Check: output, Eq: &eq}
}

// composeLayers resolves a chain the way config load stamps it, so the tests
// exercise the same construction the runtime uses.
func composeLayers(t *testing.T, defs ...config.TaskDefinition) []ResolvedLayer {
	t.Helper()
	for i := range defs[:len(defs)-1] {
		defs[i].Inner = defs[i+1].ID
	}
	outer := defs[0]
	outer.InnerChain = defs[1:]
	layers, err := ResolveLayers(outer)
	if err != nil {
		t.Fatalf("ResolveLayers: %v", err)
	}
	return layers
}

// TestComposeDoneWhen_ConjoinsEveryLayerInnermostFirst covers the completion
// contract of a chain: the inner effective done_when with the outer's
// additions after it, every leaf still necessary, and each leaf attributed to
// the layer that declared it.
func TestComposeDoneWhen_ConjoinsEveryLayerInnermostFirst(t *testing.T) {
	layers := composeLayers(t,
		config.TaskDefinition{ID: "outer", Scope: "run",
			DoneWhen: doneWhen(config.DoneWhenLeaf{Judge: "release checklist", ID: "release-gate"}),
			Bind:     &config.BindConfig{Outputs: map[string]string{"decision": "{{.Inner.outputs.review_decision}}"}},
		},
		config.TaskDefinition{ID: "work", Scope: "run",
			DoneWhen: doneWhen(checkLeaf("review_decision", "APPROVED")),
		},
	)
	dw, owners := ComposeDoneWhen(layers)
	if dw == nil || len(dw.All) != 2 {
		t.Fatalf("composed done_when = %+v, want both layers' leaves", dw)
	}
	if dw.All[0].Check == "" || dw.All[1].Judge == "" {
		t.Fatalf("composed order = %+v, want the inner condition first", dw.All)
	}
	if !reflect.DeepEqual(owners, []int{1, 0}) {
		t.Errorf("leaf owners = %v, want the inner leaf attributed to layer 1", owners)
	}
	if dw.Budget != nil {
		t.Error("the composed table must carry no budget: a budget belongs to the layer that declared it")
	}
}

// TestComposeDoneWhen_ResolvesAnInnerCheckToItsPublicName covers what makes
// an inner condition evaluable on a composed instance: the leaf keeps naming
// its own output, and resolution maps it to the name that output arrives
// under — so an outer rename does not silently break the inner author's gate.
func TestComposeDoneWhen_ResolvesAnInnerCheckToItsPublicName(t *testing.T) {
	layers := composeLayers(t,
		config.TaskDefinition{ID: "outer", Scope: "run",
			Bind: &config.BindConfig{Outputs: map[string]string{"decision": "{{.Inner.outputs.review_decision}}"}},
		},
		config.TaskDefinition{ID: "work", Scope: "run",
			DoneWhen: doneWhen(checkLeaf("review_decision", "APPROVED")),
		},
	)
	dw, _ := ComposeDoneWhen(layers)
	if got := dw.All[0].Check; got != "decision" {
		t.Errorf("inner check resolved to %q, want the public name %q", got, "decision")
	}
}

// TestComposeDoneWhen_LeavesAnUnreachableCheckUnresolved covers the runtime
// counterpart of the load-time reachability rule: an output no binding
// carries outward keeps its own name, so the leaf reads as pending rather
// than silently matching some other layer's key.
func TestComposeDoneWhen_LeavesAnUnreachableCheckUnresolved(t *testing.T) {
	layers := composeLayers(t,
		config.TaskDefinition{ID: "outer", Scope: "run"},
		config.TaskDefinition{ID: "work", Scope: "run",
			DoneWhen: doneWhen(checkLeaf("review_decision", "APPROVED")),
		},
	)
	dw, _ := ComposeDoneWhen(layers)
	if got := dw.All[0].Check; got != "review_decision" {
		t.Errorf("unreachable check resolved to %q, want it left as declared", got)
	}
	result := EvaluateTaskDoneWhen(dw, map[string]any{"decision": "APPROVED"})
	if result.Overall != DonePending {
		t.Errorf("overall = %q, want %q for a condition the contract does not carry", result.Overall, DonePending)
	}
}

// TestResolveDefinition_NestedTerminalComesFromTheDeclaringLayer covers the
// interactive endpoint: whichever layer declares [terminal], the composed
// task presents it, so attach and capture resolve through one node with no
// nesting-aware tie-break.
func TestResolveDefinition_NestedTerminalComesFromTheDeclaringLayer(t *testing.T) {
	inner := config.TaskDefinition{ID: "tmux", Scope: "run",
		Terminal: &config.TerminalConfig{Attach: "attach-it", Capture: "capture-it", SendText: "t", SendKeys: "k"},
	}
	outer := config.TaskDefinition{ID: "runtime", Scope: "run", Inner: "tmux", InnerChain: []config.TaskDefinition{inner},
		Bind: &config.BindConfig{Outputs: map[string]string{
			"interactive_endpoint": "{{.Inner.outputs.interactive_endpoint}}",
		}},
	}
	r, err := ResolveDefinition(outer, "runtime")
	if err != nil {
		t.Fatalf("ResolveDefinition: %v", err)
	}
	if r.Terminal == nil || r.Terminal.Attach != "attach-it" {
		t.Fatalf("Terminal = %+v, want the declaring layer's table", r.Terminal)
	}
	if got := TerminalLayer(r.Layers); got != 1 {
		t.Errorf("TerminalLayer = %d, want the inner layer", got)
	}
}

// TestLayerExposure_TracksRenamesAndStopsAtComputedBindings covers the map
// every cross-layer resolution rests on: a key reaches the composed contract
// only through an unbroken run of direct bindings.
func TestLayerExposure_TracksRenamesAndStopsAtComputedBindings(t *testing.T) {
	layers := composeLayers(t,
		config.TaskDefinition{ID: "outer", Scope: "run",
			Bind: &config.BindConfig{Outputs: map[string]string{
				"agent_pid": "{{.Inner.outputs.runtime_pid}}",
				"label":     "pid-{{.Inner.outputs.runtime_pid}}",
			}},
		},
		config.TaskDefinition{ID: "middle", Scope: "run",
			Bind: &config.BindConfig{Outputs: map[string]string{
				"runtime_pid": "{{.Inner.outputs.pid}}",
				"socket":      "sock:{{.Inner.outputs.socket_path}}",
			}},
		},
		config.TaskDefinition{ID: "inner", Scope: "run"},
	)
	exposure := LayerExposure(layers)
	if got := exposure[2]["pid"]; got != "agent_pid" {
		t.Errorf("innermost pid exposed as %q, want %q", got, "agent_pid")
	}
	if _, ok := exposure[2]["socket_path"]; ok {
		t.Error("a key reached only through a computed binding must not be exposed")
	}
	if got := exposure[1]["runtime_pid"]; got != "agent_pid" {
		t.Errorf("middle runtime_pid exposed as %q, want %q", got, "agent_pid")
	}
}
