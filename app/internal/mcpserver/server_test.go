package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// setUpConfigHome points config.Load()/state.NewStore("") at a scratch config
// home with a workflow whose entry resource recognizes any plain identifier
// (no "+", so a "<name>+<tag>" session name is never mistaken for one) and
// names the session after it.
func setUpConfigHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	baseDir := filepath.Join(home, ".config", "plect")
	for _, dir := range []string{"workflows", "tasks", "resources"} {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(baseDir, "config.toml"),
		[]byte("schema_version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "resources", "plain_resource.toml"),
		[]byte("[plain_resource]\nkind = \"resource\"\nmatch = '^(?P<name>[A-Za-z0-9][A-Za-z0-9/_-]*)$'\nname = { expr = \"match.name\" }\n\n[plain_resource.observe]\ntype = \"shell\"\nscript = \"echo '{}'\"\n\n[plain_resource.state_schema]\ntype = \"object\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workflows", "plain.toml"),
		[]byte("[plain]\nkind = \"workflow\"\nresource = \"plain_resource\"\n\n[[plain.nodes]]\nuses = \"noop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "tasks", "noop.toml"),
		[]byte("[noop]\nkind = \"effect\"\nscope = \"session\"\n\n[noop.setup]\ntype = \"shell\"\nscript = \"echo '{}'\"\n\n[noop.health.alive]\ntype = \"noop\"\n"), 0o644); err != nil {
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

func TestNewServer_RemovesRemovedTools(t *testing.T) {
	tools := NewServer().ListTools()
	for _, name := range []string{"plect_create", "plect_watchdog_check"} {
		if _, ok := tools[name]; ok {
			t.Fatalf("%s is still registered", name)
		}
	}
	for _, name := range []string{"plect_up", "plect_down", "plect_destroy"} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("%s is not registered", name)
		}
	}
}

// A session name is not a resource id any declaration recognizes, so plect_up
// resolves it as an existing session rather than creating one.
func TestHandleUp_ExistingSessionByName(t *testing.T) {
	setUpConfigHome(t)

	createPlainSession(t, "my-session")

	result, err := handleUp(context.Background(), reqWith(map[string]any{
		"session":  "my-session",
		"workflow": "plain",
	}))
	if err != nil {
		t.Fatalf("handleUp: %v", err)
	}
	out := decodeJSONResult(t, result)
	if out["session_name"] != "my-session" {
		t.Fatalf("session_name = %v, want my-session", out["session_name"])
	}

	store := state.NewStore("")
	s := store.Get("my-session")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if s.Workflow != "plain" {
		t.Errorf("Workflow = %q, want plain", s.Workflow)
	}
}

// The tool schemas must expose workflow/task/task_resource so a client can discover the
// arguments without reading the handler source.
func TestUpTool_ExposesWorkflowAndTask(t *testing.T) {
	for _, name := range []string{"workflow", "task", "task_resource"} {
		if _, ok := upTool.InputSchema.Properties[name]; !ok {
			t.Errorf("%s: missing %q in InputSchema.Properties", upTool.Name, name)
		}
	}
}

// task is a selection core checks, not a session input the workflow's own
// schema would judge.
func TestHandleUp_TaskSelectsAnInitialTaskCoreChecks(t *testing.T) {
	setUpConfigHome(t)

	result, err := handleUp(context.Background(), reqWith(map[string]any{
		"session":  "my-session",
		"workflow": "plain",
		"task":     "review",
	}))
	if err != nil {
		t.Fatalf("handleUp: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for a task no config layer declares")
	}
	if text := extractErrorText(result); !strings.Contains(text, "review") || !strings.Contains(text, "not found") {
		t.Errorf("error = %q, want it to report the task selection", text)
	}
}

func TestHandleUp_BySessionNameIgnoresTheServersUntrustedRoot(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	setUpConfigHome(t)
	projects, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trusted := filepath.Join(projects, "trusted")
	untrusted := filepath.Join(projects, "untrusted")
	for _, root := range []string{trusted, untrusted} {
		if err := os.MkdirAll(filepath.Join(root, ".plect"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".plect", "project.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "plect", "config.toml")
	trustedConfig := []byte("schema_version = 2\ntrusted_project_roots = [\"" + trusted + "\"]\n")
	if err := os.WriteFile(configPath, trustedConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(cfg, state.NewStore(""), service.CreateParams{URL: "widget", Workflow: "plain", Tag: "work", ProjectRoot: trusted})
	if err != nil {
		t.Fatalf("Create in the trusted root: %v", err)
	}
	t.Chdir(untrusted)

	result, err := handleUp(context.Background(), reqWith(map[string]any{"session": created.SessionName}))
	if err != nil {
		t.Fatalf("handleUp: %v", err)
	}
	if out := decodeJSONResult(t, result); out["session_name"] != created.SessionName {
		t.Fatalf("session_name = %v, want %s", out["session_name"], created.SessionName)
	}
	if after, _ := os.ReadFile(configPath); string(after) != string(trustedConfig) {
		t.Errorf("config.toml changed:\n%s", after)
	}
}

func TestHandleWorkflowList_InACheckoutUsesTheSessionVariablesRoot(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkout removed=%v", removed), func(t *testing.T) {
			checkWorkflowListInACheckout(t, removed)
		})
	}
}

func checkWorkflowListInACheckout(t *testing.T, removeCheckout bool) {
	setUpConfigHome(t)
	projects, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(projects, "repo")
	checkout := filepath.Join(projects, "checkout")
	for _, dir := range []string{root, checkout} {
		if err := os.MkdirAll(filepath.Join(dir, ".plect", "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".plect", "project.toml"), []byte("schema_version = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".plect", "workflows", "local.toml"), []byte("[local]\nkind = \"workflow\"\nresource = \"plain_resource\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "plect", "config.toml")
	trustedConfig := []byte("schema_version = 2\ntrusted_project_roots = [\"" + root + "\"]\n")
	if err := os.WriteFile(configPath, trustedConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(cfg, state.NewStore(""), service.CreateParams{URL: "widget", Workflow: "plain", Tag: "work", ProjectRoot: root})
	if err != nil {
		t.Fatalf("Create in the trusted root: %v", err)
	}
	t.Setenv("PLECT_SESSION_NAME", created.SessionName)
	t.Chdir(checkout)
	if removeCheckout {
		if err := os.RemoveAll(checkout); err != nil {
			t.Fatal(err)
		}
	}

	result, err := handleWorkflowList(context.Background(), reqWith(map[string]any{}))
	if err != nil {
		t.Fatalf("handleWorkflowList: %v", err)
	}
	if result.IsError {
		t.Fatalf("workflow list from the checkout failed: %s", extractErrorText(result))
	}
	if out := decodeJSONResult(t, result); !strings.Contains(fmt.Sprint(out["workflows"]), "local") {
		t.Errorf("workflows = %v; want the session root's workflow", out["workflows"])
	}
}
