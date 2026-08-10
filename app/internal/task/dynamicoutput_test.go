package task

import (
	"context"
	"os"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/config"
)

func TestFetchOutput_Single(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{ResourceID: "pr5", WorktreePath: t.TempDir()}}
	v, err := FetchOutput(context.Background(), &config.Config{}, config.DynamicOutput{Name: "res", Script: "printf '  {{.ResourceID}} \\n'"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["res"] != "pr5" {
		t.Errorf("values = %v, want res=pr5 (rendered + trimmed)", v)
	}
}

// One fetch yields several outputs from a JSON object — no per-field re-run.
func TestFetchOutput_Produces(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{WorktreePath: t.TempDir()}}
	src := config.DynamicOutput{
		Produces: []string{"pr_state", "review_decision", "checks"},
		Script:   `echo '{"pr_state":"OPEN","review_decision":"APPROVED","checks":3}'`,
	}
	v, err := FetchOutput(context.Background(), &config.Config{}, src, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["pr_state"] != "OPEN" || v["review_decision"] != "APPROVED" || v["checks"] != "3" {
		t.Errorf("values = %v, want the three fields (number stringified)", v)
	}
}

// A produced key the JSON omits is left unset (→ check pending), not an error.
func TestFetchOutput_ProducesMissingKeyUnset(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{WorktreePath: t.TempDir()}}
	v, err := FetchOutput(context.Background(), &config.Config{}, config.DynamicOutput{Produces: []string{"a", "b"}, Script: `echo '{"a":"x"}'`}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := v["b"]; set || v["a"] != "x" {
		t.Errorf("values = %v, want only a set", v)
	}
}

func TestFetchOutput_NonZeroExitIsFetchFailure(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{WorktreePath: t.TempDir()}}
	v, err := FetchOutput(context.Background(), &config.Config{}, config.DynamicOutput{Name: "x", Script: "echo boom >&2; exit 3"}, ctx)
	if v != nil || err == nil {
		t.Errorf("fetch failure must yield nil values + error, got v=%v err=%v", v, err)
	}
}

func TestFetchOutput_ProducesNonObjectIsError(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{WorktreePath: t.TempDir()}}
	if _, err := FetchOutput(context.Background(), &config.Config{}, config.DynamicOutput{Produces: []string{"a"}, Script: "echo not-json"}, ctx); err == nil {
		t.Error("non-JSON stdout for a produces group must error")
	}
}

func TestFetchOutput_TemplateError(t *testing.T) {
	ctx := RenderContext{Session: SessionVars{WorktreePath: t.TempDir()}}
	if _, err := FetchOutput(context.Background(), &config.Config{}, config.DynamicOutput{Name: "x", Script: "echo {{.Bogus}}"}, ctx); err == nil {
		t.Error("a bad template must error before running")
	}
}

func TestFetchOutput_FromResourceStatus(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	if err := os.MkdirAll(cfg.BaseDir+"/resources", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.BaseDir+"/resources/github.toml", []byte(`
match   = '^https://github\.com/'
observe = "echo '{\"checks_status\":\"SUCCESS\",\"issue_status\":\"NULL\"}'"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := RenderContext{Session: SessionVars{ResourceID: "https://github.com/o/r/pull/5"}}
	src := config.DynamicOutput{Produces: []string{"checks_status", "issue_status"}, FromResourceStatus: true}
	v, err := FetchOutput(context.Background(), cfg, src, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["checks_status"] != "SUCCESS" || v["issue_status"] != "NULL" {
		t.Errorf("values = %v, want the resource's observed state", v)
	}
}

func TestFetchOutput_FromResourceStatus_PassesSessionBranch(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	if err := os.MkdirAll(cfg.BaseDir+"/resources", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.BaseDir+"/resources/echo.toml", []byte(`
match   = '.*'
observe = "printf '{\"branch\":\"%s\"}' '{{.Branch}}'"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := RenderContext{Session: SessionVars{ResourceID: "x", Branch: "issue/632+claude"}}
	src := config.DynamicOutput{Produces: []string{"branch"}, FromResourceStatus: true}
	v, err := FetchOutput(context.Background(), cfg, src, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["branch"] != "issue/632+claude" {
		t.Errorf("branch = %q, want the instance's session branch threaded into observe", v["branch"])
	}
}

func TestFetchOutput_FromResourceStatus_PassesSessionWorktreePath(t *testing.T) {
	cfg := &config.Config{BaseDir: t.TempDir()}
	if err := os.MkdirAll(cfg.BaseDir+"/resources", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.BaseDir+"/resources/echo.toml", []byte(`
match   = '.*'
observe = "printf '{\"worktree_path\":\"%s\"}' '{{.WorktreePath}}'"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	ctx := RenderContext{Session: SessionVars{ResourceID: "x", WorktreePath: worktree}}
	src := config.DynamicOutput{Produces: []string{"worktree_path"}, FromResourceStatus: true}
	v, err := FetchOutput(context.Background(), cfg, src, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v["worktree_path"] != worktree {
		t.Errorf("worktree_path = %q, want the instance's session worktree path threaded into observe", v["worktree_path"])
	}
}

func TestFetchOutput_FromResourceStatus_NoBoundResourceIsError(t *testing.T) {
	cfg := &config.Config{}
	ctx := RenderContext{}
	src := config.DynamicOutput{Produces: []string{"a"}, FromResourceStatus: true}
	if _, err := FetchOutput(context.Background(), cfg, src, ctx); err == nil {
		t.Error("expected an error when the instance has no bound resource")
	}
}

func TestFetchOutput_FromResourceStatus_NoDefinitionIsError(t *testing.T) {
	cfg := &config.Config{}
	ctx := RenderContext{Session: SessionVars{ResourceID: "x"}}
	src := config.DynamicOutput{Produces: []string{"a"}, FromResourceStatus: true}
	if _, err := FetchOutput(context.Background(), cfg, src, ctx); err == nil {
		t.Error("expected an error when no resource definition recognizes the bound resource")
	}
}

func TestValidateOutputs(t *testing.T) {
	tests := []struct {
		name    string
		srcs    []config.DynamicOutput
		wantErr bool
	}{
		{"single ok", []config.DynamicOutput{{Name: "a", Script: "echo 1"}}, false},
		{"produces ok", []config.DynamicOutput{{Produces: []string{"a", "b"}, Script: "echo {}"}}, false},
		{"neither name nor produces", []config.DynamicOutput{{Script: "echo 1"}}, true},
		{"both name and produces", []config.DynamicOutput{{Name: "a", Produces: []string{"b"}, Script: "echo 1"}}, true},
		{"missing script", []config.DynamicOutput{{Name: "a"}}, true},
		{"from_resource_status ok", []config.DynamicOutput{{Produces: []string{"a"}, FromResourceStatus: true}}, false},
		{"from_resource_status and script both set", []config.DynamicOutput{{Produces: []string{"a"}, Script: "echo 1", FromResourceStatus: true}}, true},
		{"from_resource_status with name instead of produces", []config.DynamicOutput{{Name: "a", FromResourceStatus: true}}, true},
		{
			name:    "duplicate name across single and group",
			srcs:    []config.DynamicOutput{{Name: "a", Script: "echo 1"}, {Produces: []string{"a"}, Script: "echo {}"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := config.ValidateDynamicOutputs(tt.srcs); (err != nil) != tt.wantErr {
				t.Errorf("ValidateOutputs() err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestOutputSource_IsInternal(t *testing.T) {
	if !(config.DynamicOutput{}).IsInternal() {
		t.Error("script outputs default to internal")
	}
	f := false
	if (config.DynamicOutput{Internal: &f}).IsInternal() {
		t.Error("internal=false must opt out")
	}
}
