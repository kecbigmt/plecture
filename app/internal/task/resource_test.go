package task

import (
	"os"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func TestMatchResourceDef_NoMatch(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"github": {Match: `^https://github\.com/`, Observe: "echo '{}'"},
	}
	_, ok, err := MatchResourceDef(defs, "local-okf://kec/goals/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected no match")
	}
}

func TestMatchResourceDef_Ambiguous(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"a": {Match: `^https://`, Observe: "echo '{}'"},
		"b": {Match: `github\.com`, Observe: "echo '{}'"},
	}
	_, _, err := MatchResourceDef(defs, "https://github.com/o/r/pull/1")
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("expected ambiguous-match error, got %v", err)
	}
}

func TestResourceStatus_NoDefinition(t *testing.T) {
	state, _, ok, err := ResourceStatus(nil, "https://github.com/o/r/pull/1", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok || state != nil {
		t.Errorf("expected ok=false, state=nil; got ok=%v state=%v", ok, state)
	}
}

func TestResourceStatus_ObservesAndValidates(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"github": {
			ID:      "github",
			Match:   `^https://github\.com/`,
			Observe: `echo '{"resource_kind":"pull","checks_status":"SUCCESS"}'`,
			StateSchema: map[string]any{
				"type":     "object",
				"required": []any{"resource_kind"},
				"properties": map[string]any{
					"resource_kind": map[string]any{"type": "string"},
					"checks_status": map[string]any{"type": "string"},
				},
			},
		},
	}
	state, def, ok, err := ResourceStatus(defs, "https://github.com/o/r/pull/5", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || def.ID != "github" {
		t.Fatalf("expected match on github def, got ok=%v def=%+v", ok, def)
	}
	if state["resource_kind"] != "pull" || state["checks_status"] != "SUCCESS" {
		t.Errorf("state = %v, want resource_kind=pull checks_status=SUCCESS", state)
	}
}

func TestResourceStatus_ObserveTemplateSeesResourceID(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"echo": {Match: `.*`, Observe: `printf '{"resource_id":"%s"}' '{{.ResourceID}}'`},
	}
	state, _, ok, err := ResourceStatus(defs, "local-okf://kec/goals/x.md", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || state["resource_id"] != "local-okf://kec/goals/x.md" {
		t.Errorf("state = %v, want the observed resource id echoed back", state)
	}
}

func TestResourceStatus_ObserveTemplateSeesBranch(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"echo": {Match: `.*`, Observe: `printf '{"branch":"%s"}' '{{.Branch}}'`},
	}
	state, _, ok, err := ResourceStatus(defs, "local-okf://kec/goals/x.md", "issue/632+claude", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || state["branch"] != "issue/632+claude" {
		t.Errorf("state = %v, want the passed branch echoed back", state)
	}
}

func TestResourceStatus_ObserveTemplateSeesWorkdirPath(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"echo": {Match: `.*`, Observe: `printf '{"workdir_path":"%s"}' '{{.WorkdirPath}}'`},
	}
	state, _, ok, err := ResourceStatus(defs, "local-okf://kec/goals/x.md", "", "/tmp/wt/issue-632", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || state["workdir_path"] != "/tmp/wt/issue-632" {
		t.Errorf("state = %v, want the passed workdir path echoed back", state)
	}
}

func TestResourceStatus_SchemaViolationErrors(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"github": {
			Match:   `.*`,
			Observe: `echo '{"resource_kind":123}'`,
			StateSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"resource_kind": map[string]any{"type": "string"},
				},
			},
		},
	}
	if _, _, _, err := ResourceStatus(defs, "x", "", "", nil); err == nil {
		t.Error("expected a state_schema validation error")
	}
}

func TestResourceStatus_NonZeroExitIsError(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"x": {Match: `.*`, Observe: "echo boom >&2; exit 1"},
	}
	if _, _, _, err := ResourceStatus(defs, "x", "", "", nil); err == nil {
		t.Error("expected observe failure to error")
	}
}

func TestFinalizeResource_NoDefinition(t *testing.T) {
	ran, _, err := FinalizeResource(nil, FinalizeResourceParams{ResourceID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("expected ran=false when no resource definition matches")
	}
}

func TestFinalizeResource_NoFinalizeScriptIsNoop(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"github": {ID: "github", Match: `.*`, Observe: "echo '{}'"},
	}
	ran, def, err := FinalizeResource(defs, FinalizeResourceParams{ResourceID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if ran || def.ID != "github" {
		t.Errorf("expected ran=false with the matched definition, got ran=%v def=%+v", ran, def)
	}
}

func TestFinalizeResource_RunsAndSeesEvidence(t *testing.T) {
	out := t.TempDir() + "/finalize.out"
	defs := map[string]config.ResourceDef{
		"local-okf": {
			ID:      "local-okf",
			Match:   `.*`,
			Observe: "echo '{}'",
			Finalize: `cat > ` + out + ` <<EOF
resource={{.ResourceID}}
instance={{.Instance}}
session={{.SessionName}}
revision={{.Revision}}
judges={{.JudgesJSON}}
EOF`,
		},
	}
	ran, def, err := FinalizeResource(defs, FinalizeResourceParams{
		ResourceID:  "local-okf://kec/goals/x.md",
		Instance:    "goal_x",
		SessionName: "kec/_orchestrator",
		Revision:    "sha256:abc",
		Judges: []FinalizeJudgeEvidence{
			{ID: "goal-met", Reason: "checklist complete", Revision: "sha256:abc", ReviewerSession: "kec/_orchestrator+review"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ran || def.ID != "local-okf" {
		t.Fatalf("expected ran=true on local-okf, got ran=%v def=%+v", ran, def)
	}
	raw, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	data := string(raw)
	for _, want := range []string{
		"resource=local-okf://kec/goals/x.md",
		"instance=goal_x",
		"session=kec/_orchestrator",
		"revision=sha256:abc",
		`"id":"goal-met"`,
		`"reviewer_session":"kec/_orchestrator+review"`,
	} {
		if !strings.Contains(data, want) {
			t.Errorf("finalize output = %q, want it to contain %q", data, want)
		}
	}
}

func TestFinalizeResource_ScriptFailureErrors(t *testing.T) {
	defs := map[string]config.ResourceDef{
		"x": {Match: `.*`, Observe: "echo '{}'", Finalize: "echo boom >&2; exit 1"},
	}
	if _, _, err := FinalizeResource(defs, FinalizeResourceParams{ResourceID: "x"}); err == nil {
		t.Error("expected finalize script failure to error")
	}
}
