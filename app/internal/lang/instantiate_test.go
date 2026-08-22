package lang

import (
	"errors"
	"strings"
	"testing"
)

const goalTaskDocument = `+++
[pursue_goal]
kind              = "task"
resource_observer = "goal"

[pursue_goal.done_when]
all = [{ check = "resource.state.goal_status", in = ["open"] }]
+++
Pursue the goal at {{ resource.id }}.
`

const goalObserverContext = `[goal]
kind  = "resource_observer"
match = '^local-okf://[A-Za-z0-9-]+/goals/[A-Za-z0-9._/-]+\.md$'

[goal.observe]
type = "exec"
bin  = "okf-goal"
args = ["observe"]

[goal.state_schema]
type = "object"

[goal.state_schema.properties]
goal_status = { type = "string" }
`

const goalResourceID = "local-okf://acme/goals/ship.md"

func instantiateGoalTask(t *testing.T, resourceID string, observe ObserveFunc) (*Instance, error) {
	t.Helper()
	context, err := ParseDefinitionDocument("context.toml", []byte(goalObserverContext))
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseTaskDocument("task.md", []byte(goalTaskDocument))
	if err != nil {
		t.Fatal(err)
	}
	v := Validation{From: Ownership{IsPlugin: true, Alias: fixtureAlias, Path: fixturePath}}
	registry := NewRegistry([]PluginLayer{{Alias: fixtureAlias, Path: fixturePath, Defs: append(context, def)}}, nil)
	return v.Instantiate(def, registry, resourceID, observe)
}

func observedState(state map[string]any) ObserveFunc {
	return func(*Definition, string) (map[string]any, error) { return state, nil }
}

func TestInstantiateBindsToTheDeclaredObserver(t *testing.T) {
	instance, err := instantiateGoalTask(t, goalResourceID, observedState(map[string]any{"goal_status": "open"}))
	if err != nil {
		t.Fatalf("a resource the declared observer recognizes binds: %v", err)
	}
	state, err := instance.State()
	if err != nil {
		t.Fatalf("the first observation succeeded: %v", err)
	}
	if state["goal_status"] != "open" {
		t.Errorf("the instance reads the state its first observation published: %v", state)
	}
}

func TestInstantiateRejectsAResourceOfAnotherKind(t *testing.T) {
	_, err := instantiateGoalTask(t, "https://github.com/acme/repo/issues/7", observedState(nil))
	wantDiag(t, err, CodeResourceObserverMismatch, LayerInstantiation)
}

func TestInstantiateFailsClosedOnTheFirstObservation(t *testing.T) {
	observe := func(*Definition, string) (map[string]any, error) {
		return nil, errors.New("goal file does not parse")
	}
	_, err := instantiateGoalTask(t, goalResourceID, observe)
	wantDiag(t, err, CodeFirstObserveFailed, LayerInstantiation)
	var d *Diagnostic
	if errors.As(err, &d) && !strings.Contains(d.Reason, "goal file does not parse") {
		t.Errorf("the observer's own error is what the caller sees: %q", d.Reason)
	}
}

// A later observation degrades: the instance created by a successful first
// observation survives one that fails, and reads as unobserved rather than
// keeping a snapshot its resource no longer supports.
func TestInstanceObserveDegradesAfterTheFirst(t *testing.T) {
	instance, err := instantiateGoalTask(t, goalResourceID, observedState(map[string]any{"goal_status": "open"}))
	if err != nil {
		t.Fatal(err)
	}
	instance.Observe(func(*Definition, string) (map[string]any, error) {
		return nil, errors.New("goal file does not parse")
	})
	state, err := instance.State()
	if state != nil {
		t.Errorf("a degraded instance reads as unobserved, not as its last snapshot: %v", state)
	}
	if err == nil || !strings.Contains(err.Error(), "goal file does not parse") {
		t.Errorf("the degradation carries the observer's own error: %v", err)
	}

	instance.Observe(observedState(map[string]any{"goal_status": "completed"}))
	state, err = instance.State()
	if err != nil {
		t.Fatalf("the next success clears the degradation: %v", err)
	}
	if state["goal_status"] != "completed" {
		t.Errorf("got %v", state)
	}
}
