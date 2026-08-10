package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/kecbigmt/plect/app/internal/state"
)

// setUpConfigHome points config.Load()/state.NewStore("") at a scratch config
// home with a resolver-less workflow ("plain": no [resolver], so dispatch
// treats the identifier as the session id directly — see
// TestDispatchResource_FlagWithoutResolverIsIdentityForCaller in the service
// package). MCP tws_create/tws_up previously had no "workflow" argument, so
// this workflow was unreachable from MCP.
func setUpConfigHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	baseDir := filepath.Join(home, ".config", "tws")
	for _, dir := range []string{"workflows", "tasks", "providers"} {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(baseDir, "config.toml"),
		[]byte("worktrees_root = \""+filepath.Join(home, "worktrees")+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(home, "wd")
	if err := os.WriteFile(filepath.Join(baseDir, "providers", "plain.toml"),
		[]byte("setup = \"mkdir -p "+workdir+" && echo '{\\\"workdir\\\":\\\""+workdir+"\\\"}'\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workflows", "plain.toml"),
		[]byte("provider = \"plain\"\n\n[[nodes]]\nid = \"noop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "tasks", "noop.toml"),
		[]byte("scope = \"session\"\nsetup = \"echo '{}'\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeJSONResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractErrorText(result))
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}

// A resolver-less workflow requires an explicit --workflow, and tws_create
// can now supply it.
func TestHandleCreate_ResolverLessWorkflow(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleCreate(context.Background(), reqWith(map[string]any{
		"url":      "my-session",
		"workflow": "plain",
	}))
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	out := decodeJSONResult(t, result)
	if out["session_name"] != "my-session+plain" {
		t.Fatalf("session_name = %v, want my-session+plain (resolver-less identity, tag defaults to workflow id)", out["session_name"])
	}

	store := state.NewStore("")
	s := store.Get("my-session+plain")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if s.Workflow != "plain" {
		t.Errorf("Workflow = %q, want plain", s.Workflow)
	}
}

// A resolver-less workflow's identifier isn't a URL, so tws_up's
// docker-compose-up-style auto-create only kicks in for resolver-matched or
// URL identifiers (service.Up); a bare resolver-less identifier must already
// have a session (created via tws_create). Once it exists, tws_up must still
// resolve and run it — exercising the same "workflow" argument tws_up now
// accepts.
func TestHandleUp_ResolverLessWorkflowExistingSession(t *testing.T) {
	setUpConfigHome(t)

	if _, err := handleCreate(context.Background(), reqWith(map[string]any{
		"url":      "my-session",
		"workflow": "plain",
	})); err != nil {
		t.Fatalf("handleCreate: %v", err)
	}

	result, err := handleUp(context.Background(), reqWith(map[string]any{
		"session":  "my-session+plain",
		"workflow": "plain",
	}))
	if err != nil {
		t.Fatalf("handleUp: %v", err)
	}
	out := decodeJSONResult(t, result)
	if out["session_name"] != "my-session+plain" {
		t.Fatalf("session_name = %v, want my-session+plain", out["session_name"])
	}

	store := state.NewStore("")
	s := store.Get("my-session+plain")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if s.Workflow != "plain" {
		t.Errorf("Workflow = %q, want plain", s.Workflow)
	}
}

// The tool schemas must expose workflow/task so a client can discover the
// arguments without reading the handler source.
func TestCreateAndUpTools_ExposeWorkflowAndTask(t *testing.T) {
	for _, tool := range []mcp.Tool{createTool, upTool} {
		for _, name := range []string{"workflow", "task"} {
			if _, ok := tool.InputSchema.Properties[name]; !ok {
				t.Errorf("%s: missing %q in InputSchema.Properties", tool.Name, name)
			}
		}
	}
}

// task is pure syntax sugar over inputs.task — tws_create must merge
// it into the session's persisted inputs like the CLI's --task.
func TestHandleCreate_TaskShorthand(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleCreate(context.Background(), reqWith(map[string]any{
		"url":      "my-session",
		"workflow": "plain",
		"task":     "review",
	}))
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	decodeJSONResult(t, result)

	store := state.NewStore("")
	s := store.Get("my-session+plain")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if s.Inputs["task"] != "review" {
		t.Errorf("Inputs[task] = %v, want review", s.Inputs["task"])
	}
}

// A task value conflicting with inputs.task must fail rather than silently
// picking one — same rule the CLI/Web UI shorthand follows.
func TestHandleCreate_TaskConflictsWithInputs(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleCreate(context.Background(), reqWith(map[string]any{
		"url":      "my-session",
		"workflow": "plain",
		"task":     "review",
		"inputs":   map[string]any{"task": "work"},
	}))
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for conflicting task")
	}
}
