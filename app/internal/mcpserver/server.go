package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/github"
	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/template"
	"github.com/kecbigmt/sennit/app/internal/workspace"
)

// NewServer creates a new MCP server with sennit tools registered.
func NewServer() *server.MCPServer {
	s := server.NewMCPServer(
		"sennit",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	wrap := func(name string, h server.ToolHandlerFunc) server.ToolHandlerFunc {
		return instrumentHandler(name, h)
	}

	s.AddTools(
		server.ServerTool{Tool: createTool, Handler: wrap("sennit_create", handleCreate)},
		server.ServerTool{Tool: upTool, Handler: wrap("sennit_up", handleUp)},
		server.ServerTool{Tool: downTool, Handler: wrap("sennit_down", handleDown)},
		server.ServerTool{Tool: destroyTool, Handler: wrap("sennit_destroy", handleDestroy)},
		server.ServerTool{Tool: statusTool, Handler: wrap("sennit_status", handleStatus)},
		server.ServerTool{Tool: captureTool, Handler: wrap("sennit_capture", handleCapture)},
		server.ServerTool{Tool: listTool, Handler: wrap("sennit_list", handleList)},
		server.ServerTool{Tool: templateListTool, Handler: wrap("sennit_template_list", handleTemplateList)},
		server.ServerTool{Tool: workflowListTool, Handler: wrap("sennit_workflow_list", handleWorkflowList)},
		server.ServerTool{Tool: workflowShowTool, Handler: wrap("sennit_workflow_show", handleWorkflowShow)},
		server.ServerTool{Tool: gcTool, Handler: wrap("sennit_gc", handleGC)},
		server.ServerTool{Tool: eventListTool, Handler: wrap("sennit_event_list", handleEventList)},
		server.ServerTool{Tool: eventShowTool, Handler: wrap("sennit_event_show", handleEventShow)},
		server.ServerTool{Tool: eventPublishTool, Handler: wrap("sennit_event_publish", handleEventPublish)},
		server.ServerTool{Tool: taskSetupTool, Handler: wrap("sennit_task_setup", handleTaskSetup)},
		server.ServerTool{Tool: taskCleanupTool, Handler: wrap("sennit_task_cleanup", handleTaskCleanup)},
		server.ServerTool{Tool: taskFinalizeTool, Handler: wrap("sennit_task_finalize", handleTaskFinalize)},
		server.ServerTool{Tool: resourceStatusTool, Handler: wrap("sennit_resource_status", handleResourceStatus)},
		server.ServerTool{Tool: checkTool, Handler: wrap("sennit_check", handleCheck)},
		server.ServerTool{Tool: tickTool, Handler: wrap("sennit_tick", handleTick)},
		server.ServerTool{Tool: watchdogCheckTool, Handler: wrap("sennit_watchdog_check", handleWatchdogCheck)},
		server.ServerTool{Tool: judgeApproveTool, Handler: wrap("sennit_judge_approve", handleJudgeApprove)},
		server.ServerTool{Tool: judgeRequestChangesTool, Handler: wrap("sennit_judge_request_changes", handleJudgeRequestChanges)},
		server.ServerTool{Tool: subscribeTool, Handler: wrap("sennit_subscribe", handleSubscribe)},
	)

	return s
}

var createTool = mcp.NewTool("sennit_create",
	mcp.WithDescription("Create a session for a resource identifier and run session-scoped tasks. A workflow whose [resolver] matches the identifier is auto-selected (GitHub issue/PR URLs and PVTI_xxx project items today); other identifiers need an explicit workflow. Does not start the runtime — call sennit_up to launch run-scoped tasks."),
	mcp.WithString("url",
		mcp.Required(),
		mcp.Description("Resource identifier the active workflow's [resolver] accepts. GitHub Issue/PR URLs (e.g. https://github.com/owner/repo/pull/123) and project item IDs (e.g. PVTI_xxx) resolve out of the box; other identifiers need an explicit workflow. The JSON key stays \"url\" for backward compatibility."),
	),
	mcp.WithString("tag",
		mcp.Description("Workspace-identity label for the session. Omit it to default to the workflow id, so two tools on one resource (e.g. claude work, codex review) get separate workspaces automatically; pass it to label a retry/parallel session (e.g. \"review\" → owner/repo-123+review). Provider-agnostic — the GitHub provider maps it to a branch/worktree suffix."),
	),
	mcp.WithString("parent",
		mcp.Description("Parent session name for the session tree. Defaults to SENNIT_SESSION_NAME when it names an existing session."),
	),
	mcp.WithObject("inputs",
		mcp.Description("Session inputs as a JSON object. Validated against [inputs_schema] in the active workflow file (or top-level config.toml for the legacy inline-tasks path), frozen at create time, and exposed to task templates as {{.Inputs.<key>}}."),
	),
	mcp.WithString("workflow",
		mcp.Description("Workflow id (filename stem of .sennit/workflows/<id>.toml). Required when the identifier has no matching [resolver]; with a resolver-less workflow the identifier itself becomes the session id."),
	),
	mcp.WithString("task",
		mcp.Description("Shorthand for inputs.task — sets the session's initial task id (e.g. \"work\", \"review\"). Merged into inputs; conflicts with a different \"task\" already set there. Workflows that declare task as required (e.g. claude, codex) reject create without it; pass \"none\" for an ad-hoc session with no initial instruction."),
	),
)

var upTool = mcp.NewTool("sennit_up",
	mcp.WithDescription("Run run-scoped tasks for a session, in dependency-respecting order. When the identifier is a resource identifier and no state entry exists (or a session-scoped task has not yet reached \"produced\"), sennit_create is invoked first (docker compose up-style auto-create)."),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier (e.g. a GitHub Issue/PR URL) or session name (e.g. owner/repo-123)"),
	),
	mcp.WithString("tag",
		mcp.Description("Workspace-identity label of the session to resolve/auto-create (resource-identifier only, not a bare session name). Defaults to the workflow id, matching sennit_create."),
	),
	mcp.WithString("parent",
		mcp.Description("Parent session name for auto-created sessions. Defaults to SENNIT_SESSION_NAME when it names an existing session."),
	),
	mcp.WithObject("inputs",
		mcp.Description("Session inputs forwarded to the auto-create path. Rejected when the session already exists."),
	),
	mcp.WithString("workflow",
		mcp.Description("Workflow id forwarded to the auto-create path (resolver-less workflows need this). Rejected when the session already exists."),
	),
	mcp.WithString("task",
		mcp.Description("Shorthand for inputs.task, forwarded to the auto-create path; see sennit_create's task parameter. Rejected when the session already exists."),
	),
)

var downTool = mcp.NewTool("sennit_down",
	mcp.WithDescription("Run run-scoped cleanup for a session in reverse dependency order. Session-scoped tasks are preserved."),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier (e.g. a GitHub Issue/PR URL) or session name (e.g. owner/repo-123)"),
	),
)

var destroyTool = mcp.NewTool("sennit_destroy",
	mcp.WithDescription("Tear down a session: run-scoped cleanup (auto-down) → session-scoped cleanup → worktree removal → state entry deletion. Fail-fast by default; --force demotes cleanup errors to warnings so a stuck session can be freed. Also fails closed (code has_children) if the session has child sessions, since deleting it would orphan them; --force orphans them instead, reported via cleanup_warnings."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{DestructiveHint: boolPtr(true)}),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier (e.g. a GitHub Issue/PR URL) or session name (e.g. owner/repo-123)"),
	),
	mcp.WithBoolean("force",
		mcp.Description("Demote cleanup errors to warnings so teardown continues through worktree + state deletion; also passes --force to git worktree remove so a dirty worktree can be removed; and proceeds when the session has child sessions, orphaning them instead of aborting"),
	),
	mcp.WithBoolean("delete_branch",
		mcp.Description("Also delete the local branch after removing the worktree"),
	),
)

var statusTool = mcp.NewTool("sennit_status",
	mcp.WithDescription("Report a session's four fact layers: identity (resource id / workflow / tag / tree position), runtime (declared-healthcheck liveness, worktree existence, run-scoped task state), work (each task instance's outputs, done_when evaluation, round budget, and chain plan), and flow (recent events). No provider-specific field — everything renders generically from outputs / done_when / events."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("url",
		mcp.Required(),
		mcp.Description("Resource identifier (e.g. a GitHub Issue/PR URL) or session name (e.g. owner/repo-123)"),
	),
	mcp.WithBoolean("refresh",
		mcp.Description("Re-fetch dynamic outputs from the source of truth before reporting, so the reported done_when reflects current state rather than the last persisted value."),
	),
)

var captureTool = mcp.NewTool("sennit_capture",
	mcp.WithDescription("Read-only snapshot of a session's channel: resolves the task declaring 'capture' (symmetric with attach's 'attach' declaration) and returns its rendered template's output as-is. No interpretation — the raw view is left for the caller to judge. Session state never changes."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier (e.g. a GitHub Issue/PR URL) or session name (e.g. owner/repo-123)"),
	),
)

var listTool = mcp.NewTool("sennit_list",
	mcp.WithDescription("List all sessions with lifecycle status and provider-specific status (GitHub status when present)"),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
)

var gcTool = mcp.NewTool("sennit_gc",
	mcp.WithDescription("Identify and remove stale sessions. By default returns a dry-run preview. Set execute=true to perform cleanup. Completion is judged by each session's done_when-bearing task instances over persisted outputs; sessions without such tasks are left alone. Dynamic outputs are refreshed explicitly before decision points, not by gc. Deletion goes through a non-force destroy, so task cleanups run and a dirty worktree blocks it."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{DestructiveHint: boolPtr(true)}),
	mcp.WithBoolean("execute",
		mcp.Description("Actually perform cleanup. When false (default), returns what would be deleted without making changes."),
	),
	mcp.WithBoolean("delete_branch",
		mcp.Description("Also delete local branches for auto-deleted sessions"),
	),
)

var templateListTool = mcp.NewTool("sennit_template_list",
	mcp.WithDescription("List available prompt templates with descriptions. Use this to discover templates before composing task inputs."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("repo",
		mcp.Description("owner/repo to include repo-specific templates (e.g. owner/repo)"),
	),
)

var workflowListTool = mcp.NewTool("sennit_workflow_list",
	mcp.WithDescription("List workflows discoverable via the .sennit/workflows cascade. Each entry surfaces id/name/description so an agent can pick the right workflow before calling sennit_workflow_show or sennit_create."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
)

var workflowShowTool = mcp.NewTool("sennit_workflow_show",
	mcp.WithDescription("Return full details for a single workflow: id, name, description, the (merged) inputs_schema, and the compiled DAG of nodes. Use inputs_schema to build the --inputs payload for sennit_create / sennit_up."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("id",
		mcp.Required(),
		mcp.Description("Workflow id (matches the .toml filename stem)"),
	),
)

func handleWorkflowList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	cwd, err := os.Getwd()
	if err != nil {
		return errorResult(err), nil
	}
	workflows, err := service.WorkflowList(cfg, cwd)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]any{
		"ok":        true,
		"workflows": workflows,
	})
}

func handleWorkflowShow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	id := request.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errorResult(err), nil
	}
	detail, err := service.WorkflowShow(cfg, cwd, id)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]any{
		"ok":       true,
		"workflow": detail,
	})
}

func handleTemplateList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()

	repo := request.GetString("repo", "")

	repoDir := ""
	if repo != "" {
		mgr := workspace.NewManager(cfg.WorktreesRoot)
		repoDir = mgr.RepoDir(github.RepoSlug(repo))
	}
	templates, err := template.List(repoDir)
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":        true,
		"templates": templates,
	})
}

func handleCreate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	url := request.GetString("url", "")
	if url == "" {
		return mcp.NewToolResultError("url is required"), nil
	}

	inputs, err := service.MergeTaskInput(getObjectArg(request, "inputs"), request.GetString("task", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := service.Create(cfg, store, service.CreateParams{
		URL:           url,
		Tag:           request.GetString("tag", ""),
		Workflow:      request.GetString("workflow", ""),
		ParentSession: request.GetString("parent", ""),
		Inputs:        inputs,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":              true,
		"session_name":    result.SessionName,
		"worktree_path":   result.WorktreePath,
		"branch":          result.Branch,
		"reused_worktree": result.ReusedWorktree,
		"tasks":           result.Tasks,
	})
}

func handleUp(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	inputs, err := service.MergeTaskInput(getObjectArg(request, "inputs"), request.GetString("task", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := service.Up(cfg, store, service.UpParams{
		Identifier:    identifier,
		Tag:           request.GetString("tag", ""),
		Workflow:      request.GetString("workflow", ""),
		ParentSession: request.GetString("parent", ""),
		Inputs:        inputs,
	})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":           true,
		"session_name": result.SessionName,
		"tasks":        result.Tasks,
	})
}

func handleDown(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	result, err := service.Down(cfg, store, service.DownParams{Identifier: identifier})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":           true,
		"session_name": result.SessionName,
		"tasks":        result.Tasks,
	})
}

func handleDestroy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	result, err := service.Destroy(cfg, store, service.DestroyParams{
		Identifier:   identifier,
		Force:        request.GetBool("force", false),
		DeleteBranch: request.GetBool("delete_branch", false),
	})
	if err != nil {
		return errorResult(err), nil
	}

	resp := map[string]any{
		"ok":               true,
		"session_name":     result.SessionName,
		"removed_worktree": result.RemovedWorktree,
	}
	if result.WorktreeWarning != "" {
		resp["worktree_warning"] = result.WorktreeWarning
	}
	if len(result.CleanupWarnings) > 0 {
		resp["cleanup_warnings"] = result.CleanupWarnings
	}
	return jsonResult(resp)
}

func handleCapture(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	session := request.GetString("session", "")
	if session == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	result, err := service.Capture(cfg, store, service.CaptureParams{Identifier: session})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":           true,
		"session_name": result.SessionName,
		"task_id":      result.TaskID,
		"content":      result.Content,
	})
}

func handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	url := request.GetString("url", "")
	if url == "" {
		return mcp.NewToolResultError("url is required"), nil
	}

	if request.GetBool("refresh", false) {
		if _, err := service.RefreshSessionOutputs(cfg, store, url); err != nil {
			return errorResult(err), nil
		}
	}

	result, err := service.Status(cfg, store, url)
	if err != nil {
		return errorResult(err), nil
	}

	// Marshaled through the struct rather than a hand-picked field list, so a
	// future StatusResult field reaches MCP callers without a second edit here
	// going stale relative to the CLI/webui, which both serialize it as-is.
	b, err := json.Marshal(result)
	if err != nil {
		return errorResult(err), nil
	}
	var resp map[string]any
	if err := json.Unmarshal(b, &resp); err != nil {
		return errorResult(err), nil
	}
	resp["ok"] = true
	return jsonResult(resp)
}

func handleList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	entries, err := service.List(cfg, store)
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":       true,
		"sessions": entries,
	})
}

func handleGC(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg := config.Load()
	store := state.NewStore("")

	result, err := service.GC(cfg, store, service.GCParams{
		Execute:      request.GetBool("execute", false),
		DeleteBranch: request.GetBool("delete_branch", false),
	})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":       true,
		"executed": result.Executed,
		"entries":  result.Entries,
	})
}

// getObjectArg returns the named argument as a map[string]any, or nil if the
// argument is missing or not an object. Distinguishing "absent" from "{}" matters
// because Create rejects inputs against an existing session.
func getObjectArg(request mcp.CallToolRequest, name string) map[string]any {
	args := request.GetArguments()
	raw, ok := args[name]
	if !ok || raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return obj
}

// errorResult creates an MCP error result from a service error.
func errorResult(err error) *mcp.CallToolResult {
	if svcErr, ok := err.(*service.Error); ok {
		b, _ := json.Marshal(map[string]any{
			"ok":      false,
			"code":    svcErr.Code,
			"message": svcErr.Message,
		})
		return mcp.NewToolResultError(string(b))
	}
	return mcp.NewToolResultError(err.Error())
}

// jsonResult marshals data as JSON text content.
func jsonResult(data map[string]any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

func boolPtr(b bool) *bool {
	return &b
}
