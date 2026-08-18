package service

import (
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// nestedTasks builds the seeded task state of a two-layer chain instance.
func nestedTasks(composed map[string]any, innerOutputs map[string]any, layerEnv map[string]string) map[string]*contract.TaskState {
	return map[string]*contract.TaskState{
		"runtime": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "team_runtime",
			Status:  contract.TaskStatusProduced,
			Outputs: composed,
			Layers: []contract.TaskLayerState{
				{TaskID: "team_runtime", Status: contract.TaskStatusProduced, Env: layerEnv},
				{TaskID: "runtime", Status: contract.TaskStatusProduced, Outputs: innerOutputs},
			},
			SetupAt: time.Now(),
		},
	}
}

// nestedConfig writes the chain: an inner "runtime" task and an outer
// "team_runtime" wrapping it, each with whatever extra TOML the case needs.
func nestedConfig(t *testing.T, inner taskFixture, outerExtra string) *config.Config {
	t.Helper()
	inner.id = "runtime"
	if inner.scope == "" {
		inner.scope = contract.TaskScopeRun
	}
	outer := taskFixture{
		id:    "team_runtime",
		scope: contract.TaskScopeRun,
		extra: "inner = \"runtime\"\n" + outerExtra,
	}
	return writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{inner, outer},
		[]nodeFixture{{id: "runtime", uses: "team_runtime"}})
}

// bindPid re-exports the inner task's pid under the same public name, the
// minimum wiring a chain needs to expose anything at all.
const bindPid = `
[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties.pid]
type = "string"
`

// TestEvaluateHealth_NestedAliveComposesByAndNamingTheLayer covers the alive
// AND: any failing layer makes the composed task unhealthy, and the report
// says which one.
func TestEvaluateHealth_NestedAliveComposesByAndNamingTheLayer(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t, taskFixture{alive: "false"}, bindPid+"\n[health]\nalive = \"true\"\n")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(map[string]any{"pid": "1"}, map[string]any{"pid": "1"}, nil))

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.Healthy {
		t.Fatalf("report = %+v, want unhealthy — one layer's alive probe fails", report)
	}
	if !strings.Contains(report.Reason, "runtime") {
		t.Errorf("reason = %q, want it to name the failing layer", report.Reason)
	}
}

// TestEvaluateHealth_NestedLayerDeclaringNoHealthIsVacuous covers the other
// half of the AND: a layer with no `[health]` contributes nothing rather than
// counting as a failure or suppressing the layers that do declare one.
func TestEvaluateHealth_NestedLayerDeclaringNoHealthIsVacuous(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t, taskFixture{alive: "true"}, bindPid)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(map[string]any{"pid": "1"}, map[string]any{"pid": "1"}, nil))

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.Healthy || !report.Declared {
		t.Errorf("report = %+v, want healthy and declared from the one layer that declares a probe", report)
	}
}

// TestEvaluateHealth_NestedActivityComposesByOr covers the activity OR:
// evidence from any layer counts, and each layer's fingerprint is carried
// under its own name so a change anywhere is visible as movement.
func TestEvaluateHealth_NestedActivityComposesByOr(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t,
		taskFixture{activity: `echo '{"fingerprint":"inner-1"}'`},
		bindPid+"\n[health]\nactivity = \"echo '{\\\"fingerprint\\\":\\\"outer-1\\\"}'\"\n")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(map[string]any{"pid": "1"}, map[string]any{"pid": "1"}, nil))

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.ActivityDeclared {
		t.Fatalf("report = %+v, want activity declared", report)
	}
	for _, want := range []string{"inner-1", "outer-1"} {
		if !strings.Contains(report.ActivityFingerprint, want) {
			t.Errorf("fingerprint = %q, want every declaring layer to contribute (%q missing)", report.ActivityFingerprint, want)
		}
	}
}

// TestEvaluateHealth_NestedProbeRunsWithTheEnclosingBindEnv covers the last
// of the inner-owned execution surfaces: a layer's probes run with the
// environment the layers outside it inject, exactly as its setup did.
func TestEvaluateHealth_NestedProbeRunsWithTheEnclosingBindEnv(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t, taskFixture{alive: `test "$PLECT_GUARD" = on`}, bindPid)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(map[string]any{"pid": "1"}, map[string]any{"pid": "1"}, map[string]string{"PLECT_GUARD": "on"}))

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.Healthy {
		t.Errorf("report = %+v, want healthy — the inner probe should see the outer layer's bind.env", report)
	}
}

// gateSchema binds the inner task's two outputs and gives the composed task
// a contract to declare conditions against.
const gateSchema = `
[bind.outputs]
pid   = "{{.Inner.outputs.pid}}"
state = "{{.Inner.outputs.state}}"

[outputs_schema]
type = "object"

[outputs_schema.properties.pid]
type = "string"

[outputs_schema.properties.state]
type = "string"
`

// TestTick_NestedBudgetWatchesOnlyItsOwnLayersConditions covers per-layer
// patience: a heartbeat consumes a layer's budget only while that layer's own
// items are unmet, and exhausting it escalates naming that layer.
func TestTick_NestedBudgetWatchesOnlyItsOwnLayersConditions(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t,
		taskFixture{extra: "\n[done_when]\nall = [ { check = \"pid\", ne = \"\" } ]\n"},
		gateSchema+"\n[done_when]\nall = [ { check = \"state\", eq = \"done\" } ]\n\n[done_when.budget]\nheartbeat_budget = 1\n")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(
			map[string]any{"pid": "1", "state": "running"},
			map[string]any{"pid": "1", "state": "running"}, nil))

	_, computed, _, _, err := evaluateSessionActions(cfg, store, "owner/repo-1", false, TickTriggerHeartbeat)
	if err != nil {
		t.Fatalf("evaluateSessionActions: %v", err)
	}
	if len(computed) != 1 {
		t.Fatalf("computed = %+v, want one action", computed)
	}
	action := computed[0].action
	if action.Action != "escalate" {
		t.Fatalf("action = %q, want %q once the outer layer's budget is exhausted", action.Action, "escalate")
	}
	if action.Layer != "team_runtime" {
		t.Errorf("escalation layer = %q, want the layer that declared the unmet condition", action.Layer)
	}
	if len(action.LayerTicks) != 2 || action.LayerTicks[0] != 1 || action.LayerTicks[1] != 0 {
		t.Errorf("LayerTicks = %v, want only the layer with unmet items to consume patience", action.LayerTicks)
	}
	for _, item := range action.UnmetItems {
		if item.Output == "pid" {
			t.Error("the escalation carries an item the other layer declared")
		}
	}
}

// TestTick_NestedChainFiresAgainstTheComposedInstance covers a chain an inner
// layer declared: it fires against the composed instance and names the
// outputs by its own layer's names for them.
func TestTick_NestedChainFiresAgainstTheComposedInstance(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t,
		taskFixture{extra: `
[done_when]
all = [ { check = "pid", ne = "" } ]

[[chains]]
id       = "review"
workflow = "default"
[chains.when]
all = [ { check = "state", eq = "ready" } ]
[chains.inputs]
pid = "{{.Work.outputs.pid}}"
`},
		gateSchema)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(
			map[string]any{"pid": "42", "state": "ready"},
			map[string]any{"pid": "42", "state": "ready"}, nil))

	_, _, chainPlan, _, err := evaluateSessionActions(cfg, store, "owner/repo-1", false, TickTriggerHeartbeat)
	if err != nil {
		t.Fatalf("evaluateSessionActions: %v", err)
	}
	if len(chainPlan) != 1 {
		t.Fatalf("chain plan = %+v, want the inner layer's chain evaluated", chainPlan)
	}
	if !chainPlan[0].Fired {
		t.Fatalf("chain = %+v, want it to fire against the composed instance", chainPlan[0])
	}
	if got := chainPlan[0].Inputs["pid"]; got != "42" {
		t.Errorf("chain input pid = %v, want the value the composed contract carries", got)
	}
}

// TestRefreshInstanceOutputs_NestedFetchesPerLayerAndReprojects covers a
// layer's own `[[outputs]]`: the fetched value lands in that layer's contract
// and the composed contract is re-read from there rather than left stale
// beside it.
func TestRefreshInstanceOutputs_NestedFetchesPerLayerAndReprojects(t *testing.T) {
	store := testStore(t)
	cfg := nestedConfig(t, taskFixture{},
		`
[bind.outputs]
pid = "{{.Inner.outputs.pid}}"

[outputs_schema]
type = "object"

[outputs_schema.properties.pid]
type = "string"

[outputs_schema.properties.checks_status]
type = "string"

[[outputs]]
name   = "checks_status"
script = "echo SUCCESS"
`)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default",
		nestedTasks(map[string]any{"pid": "1"}, map[string]any{"pid": "1"}, nil))

	results, err := RefreshInstanceOutputs(cfg, store, "owner/repo-1", "runtime")
	if err != nil {
		t.Fatalf("RefreshInstanceOutputs: %v", err)
	}
	if len(results) != 1 || !results[0].Fetched {
		t.Fatalf("results = %+v, want the outer layer's own output fetched", results)
	}
	st := store.Get("owner/repo-1").Tasks["runtime"]
	if got := st.Layers[0].Outputs["checks_status"]; got != "SUCCESS" {
		t.Errorf("layer outputs = %v, want the fetched value on the layer that produces it", st.Layers[0].Outputs)
	}
	if got := st.Outputs["checks_status"]; got != "SUCCESS" {
		t.Errorf("composed outputs = %v, want the projection re-read after the fetch", st.Outputs)
	}
	if got := st.Outputs["pid"]; got != "1" {
		t.Errorf("composed pid = %v, want the projection to keep carrying the bound inner output", got)
	}
}
