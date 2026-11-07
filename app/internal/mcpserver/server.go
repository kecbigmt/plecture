package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// NewServer creates a new MCP server with plect tools registered.
func NewServer() *server.MCPServer {
	s := server.NewMCPServer(
		"plect",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	wrap := func(name string, h server.ToolHandlerFunc) server.ToolHandlerFunc {
		return instrumentHandler(name, h)
	}

	s.AddTools(
		server.ServerTool{Tool: upTool, Handler: wrap("plect_up", handleUp)},
		server.ServerTool{Tool: downTool, Handler: wrap("plect_down", handleDown)},
		server.ServerTool{Tool: destroyTool, Handler: wrap("plect_destroy", handleDestroy)},
		server.ServerTool{Tool: statusTool, Handler: wrap("plect_status", handleStatus)},
		server.ServerTool{Tool: captureTool, Handler: wrap("plect_capture", handleCapture)},
		server.ServerTool{Tool: listTool, Handler: wrap("plect_list", handleList)},
		server.ServerTool{Tool: workflowListTool, Handler: wrap("plect_workflow_list", handleWorkflowList)},
		server.ServerTool{Tool: workflowShowTool, Handler: wrap("plect_workflow_show", handleWorkflowShow)},
		server.ServerTool{Tool: workflowRebaseTool, Handler: wrap("plect_workflow_rebase", handleWorkflowRebase)},
		server.ServerTool{Tool: eventListTool, Handler: wrap("plect_event_list", handleEventList)},
		server.ServerTool{Tool: eventShowTool, Handler: wrap("plect_event_show", handleEventShow)},
		server.ServerTool{Tool: eventPublishTool, Handler: wrap("plect_event_publish", handleEventPublish)},
		server.ServerTool{Tool: taskSetupTool, Handler: wrap("plect_task_setup", handleTaskSetup)},
		server.ServerTool{Tool: taskCleanupTool, Handler: wrap("plect_task_cleanup", handleTaskCleanup)},
		server.ServerTool{Tool: taskFinalizeTool, Handler: wrap("plect_task_finalize", handleTaskFinalize)},
		server.ServerTool{Tool: resourceStatusTool, Handler: wrap("plect_resource_status", handleResourceStatus)},
		server.ServerTool{Tool: checkTool, Handler: wrap("plect_check", handleCheck)},
		server.ServerTool{Tool: tickTool, Handler: wrap("plect_tick", handleTick)},
		server.ServerTool{Tool: judgeApproveTool, Handler: wrap("plect_judge_approve", handleJudgeApprove)},
		server.ServerTool{Tool: judgeRequestChangesTool, Handler: wrap("plect_judge_request_changes", handleJudgeRequestChanges)},
		server.ServerTool{Tool: subscribeTool, Handler: wrap("plect_subscribe", handleSubscribe)},
	)

	return s
}

var upTool = mcp.NewTool("plect_up",
	mcp.WithDescription("Run run-scoped tasks for a session, in dependency-respecting order. When the identifier is a resource identifier and no state entry exists (or a session-scoped task has not yet reached \"produced\"), the session is created first (docker compose up-style auto-create)."),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier or session name (e.g. session-123)"),
	),
	mcp.WithString("tag",
		mcp.Description("Session-identity label of the session to resolve/auto-create (resource-identifier only, not a bare session name). Defaults to the workflow id."),
	),
	mcp.WithString("parent",
		mcp.Description("Parent session name for auto-created sessions. Defaults to PLECT_SESSION_NAME when it names an existing session."),
	),
	mcp.WithObject("inputs",
		mcp.Description("Session inputs forwarded to the auto-create path. Rejected when the session already exists."),
	),
	mcp.WithString("workflow",
		mcp.Description("Workflow id forwarded to the auto-create path (resolver-less workflows need this). Rejected when the session already exists."),
	),
	mcp.WithString("task",
		mcp.Description("Initial task to instantiate as the session's initial instance (resource-identifier only). Its resource type must claim the resource it binds; omit it for a session with no task."),
	),
	mcp.WithString("task_resource",
		mcp.Description("Concrete resource the initial task binds. Defaults to the entry resource identifier; requires task."),
	),
	mcp.WithBoolean("force_recreate",
		mcp.Description("Rebuild the session runtime for an existing session instead of resuming existing task outputs."),
	),
)

var downTool = mcp.NewTool("plect_down",
	mcp.WithDescription("Run run-scoped cleanup for a session in reverse dependency order. Session-scoped tasks are preserved."),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier or session name (e.g. session-123)"),
	),
	mcp.WithBoolean("sleep", mcp.Description("Wake on the next inbound event, subject to available capacity")),
)

// forceConfirmation is stated on both the tool and its force argument because
// a client may surface either one alone.
const forceConfirmation = "Before setting force, tell a human why destroy without it failed and get their confirmation."

const forceEffect = "force runs destroy to the end whatever stands in the way: it orphans child sessions, removes a session with no stored workflow revision, demotes cleanup failures to warnings and deletes the record without verifying the release, releases the prerequisites of a node whose cleanup failed, and tells each effect's cleanup it may override its own safeguards, such as discarding uncommitted changes in a directory it created. What those safeguards protected can be lost."

var destroyTool = mcp.NewTool("plect_destroy",
	mcp.WithDescription("Tear down a session: run-scoped cleanup (auto-down) → session-scoped cleanup → state entry deletion. Fails fast on a cleanup error and keeps the session, and fails closed (code has_children) if the session has child sessions. "+forceEffect+" "+forceConfirmation),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{DestructiveHint: boolPtr(true)}),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier or session name (e.g. session-123)"),
	),
	mcp.WithBoolean("force",
		mcp.Description("Ignore destroy's safeguards and errors and run it to the end; can lose uncommitted changes, orphan child sessions, and delete records whose release was never verified. "+forceConfirmation),
	),
)

var statusTool = mcp.NewTool("plect_status",
	mcp.WithDescription("Report a session's four fact layers: identity (resource id / workflow / tag / tree position), runtime (declared alive-probe liveness, work directory existence, run-scoped task state), work (each task instance's outputs, done_when evaluation, heartbeat budget, and chain plan), and flow (recent events). No plugin-specific field — everything renders generically from outputs / done_when / events."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("url",
		mcp.Required(),
		mcp.Description("Resource identifier or session name (e.g. session-123)"),
	),
	mcp.WithBoolean("refresh",
		mcp.Description("Observe each resource before reporting, so the reported facts are current as of this call rather than as of the last observation. Without it, reporting renders the last observation and says when it was taken."),
	),
)

var captureTool = mcp.NewTool("plect_capture",
	mcp.WithDescription("Read-only snapshot of a session's channel: resolves the task declaring 'capture' (symmetric with attach's 'attach' declaration) and returns its rendered template's output as-is. No interpretation — the raw view is left for the caller to judge. Session state never changes."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("session",
		mcp.Required(),
		mcp.Description("Resource identifier or session name (e.g. session-123)"),
	),
)

var listTool = mcp.NewTool("plect_list",
	mcp.WithDescription("List all sessions with lifecycle status and the workflow's own display status when present"),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
)

var workflowListTool = mcp.NewTool("plect_workflow_list",
	mcp.WithDescription("List workflows discoverable via the .plect/workflows cascade. Each entry surfaces id/name/description so an agent can pick the right workflow before calling plect_workflow_show or plect_up."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
)

var workflowShowTool = mcp.NewTool("plect_workflow_show",
	mcp.WithDescription("Return full details for a single workflow: id, name, description, the (merged) inputs_schema, and the compiled DAG of nodes. Use inputs_schema to build the --inputs payload for plect_up."),
	mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
	mcp.WithString("id",
		mcp.Required(),
		mcp.Description("Workflow id (matches the .toml filename stem)"),
	),
)

var workflowRebaseTool = mcp.NewTool("plect_workflow_rebase",
	mcp.WithDescription("Classify and apply the latest workflow revision to one session. A dry run reports compatibility and nodes to recreate without changing state. Run-scoped declaration changes require with_restart; session-scoped changes require recreation."),
	mcp.WithString("session", mcp.Required(), mcp.Description("Existing session name or resource identifier")),
	mcp.WithBoolean("dry_run", mcp.Description("Classify without changing the session")),
	mcp.WithBoolean("with_restart", mcp.Description("Allow old-revision down followed by new-revision up")),
)

func handleWorkflowRebase(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := request.GetString("session", "")
	if name == "" {
		return mcp.NewToolResultError("session is required"), nil
	}
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
	result, err := service.WorkflowRebase(cfg, state.NewStore(""), service.RebaseParams{
		Identifier: name, DryRun: request.GetBool("dry_run", false), WithRestart: request.GetBool("with_restart", false),
	})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]any{"ok": true, "session_name": result.SessionName, "from": result.From, "to": result.To, "class": result.Class, "recreate_nodes": result.RecreateNodes, "reasons": result.Reasons})
}

// callerDir is the MCP server's own working directory, which with its trusted
// ancestors contributes the `.plect/` overlays a direct invocation reads; an
// unreadable one selects none.
func callerDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

func handleWorkflowList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
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
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
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

func handleUp(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	result, err := service.Up(cfg, store, service.UpParams{
		Identifier:    identifier,
		Tag:           request.GetString("tag", ""),
		Workflow:      request.GetString("workflow", ""),
		ParentSession: request.GetString("parent", ""),
		Inputs:        getObjectArg(request, "inputs"),
		InitialTask:   service.InitialTaskSelection(request.GetString("task", ""), request.GetString("task_resource", "")),
		ForceRecreate: request.GetBool("force_recreate", false),
		InvocationDir: callerDir(),
	})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":             true,
		"result":         result.Result,
		"queue_position": result.QueuePosition,
		"session_name":   result.SessionName,
		"tasks":          result.Tasks,
	})
}

func handleDown(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	result, err := service.Down(cfg, store, service.DownParams{Identifier: identifier, Sleep: request.GetBool("sleep", false)})
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
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
	store := state.NewStore("")

	identifier := request.GetString("session", "")
	if identifier == "" {
		return mcp.NewToolResultError("session is required"), nil
	}

	if _, ok := request.GetArguments()["cleanup_inputs"]; ok {
		return mcp.NewToolResultError(fmt.Sprintf("cleanup_inputs is retired: what a cleanup releases is decided when the session is created; see %s", lang.WorkspaceProviderMigrationGuide)), nil
	}

	result, err := service.Destroy(cfg, store, service.DestroyParams{
		Identifier: identifier,
		Force:      request.GetBool("force", false),
	})
	if err != nil {
		return errorResult(err), nil
	}

	resp := map[string]any{
		"ok":           true,
		"session_name": result.SessionName,
	}
	if len(result.CleanupWarnings) > 0 {
		resp["cleanup_warnings"] = result.CleanupWarnings
	}
	return jsonResult(resp)
}

func handleCapture(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
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
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
	store := state.NewStore("")

	url := request.GetString("url", "")
	if url == "" {
		return mcp.NewToolResultError("url is required"), nil
	}

	if request.GetBool("refresh", false) {
		if _, err := service.ObserveSessionResources(cfg, store, url); err != nil {
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
	cfg, err := config.Load()
	if err != nil {
		return errorResult(err), nil
	}
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
