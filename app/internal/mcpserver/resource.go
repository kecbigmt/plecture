package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/service"
)

var resourceStatusTool = mcp.NewTool("sennit_resource_status",
	mcp.WithDescription("Observe a resource by resource id: finds the trusted resource definition (resources/*.toml) whose match recognizes it and runs its observe script. The same observation contract a task instance's from_resource_status dynamic output reads from — this lets it be read standalone, outside any one task instance. A resource id with no matching definition is an error."),
	mcp.WithString("resource_id",
		mcp.Required(),
		mcp.Description("Resource id to observe"),
	),
)

func handleResourceStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resourceID := request.GetString("resource_id", "")
	if resourceID == "" {
		return mcp.NewToolResultError("resource_id is required"), nil
	}

	result, err := service.ResourceStatus(config.Load(), service.ResourceStatusParams{ResourceID: resourceID})
	if err != nil {
		return errorResult(err), nil
	}

	return jsonResult(map[string]any{
		"ok":          true,
		"resource_id": result.ResourceID,
		"definition":  result.Definition,
		"state":       result.State,
	})
}
