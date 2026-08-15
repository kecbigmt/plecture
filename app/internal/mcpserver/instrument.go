package mcpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kecbigmt/plecture/app/internal/traceid"
)

type traceIDKey struct{}

// traceIDFromContext retrieves the trace ID stored by the instrumentation wrapper.
func traceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey{}).(string); ok {
		return v
	}
	return ""
}

// instrumentHandler wraps an MCP tool handler with structured logging.
// It generates a trace_id, measures duration, and logs the call via slog.
func instrumentHandler(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tid := traceid.Generate()
		ctx = context.WithValue(ctx, traceIDKey{}, tid)
		start := time.Now()

		result, err := handler(ctx, request)

		durationMs := time.Since(start).Milliseconds()

		status := "ok"
		var errMsg string

		if err != nil {
			status = "error"
			errMsg = err.Error()
		} else if result != nil && result.IsError {
			status = "error"
			errMsg = extractErrorText(result)
		}

		// Label the log line with whatever identity the call carried. The
		// "url" argument is a resource identifier and the "session" argument
		// is a session identifier; both are logged verbatim, because turning
		// a resource identifier into a session name is the provider
		// resolver's job and must not be duplicated here.
		resource := request.GetString("url", "")
		session := request.GetString("session", "")

		// Build slog attributes.
		attrs := []slog.Attr{
			slog.String("component", "plect-mcp"),
			slog.String("event", "mcp_call"),
			slog.String("tool", toolName),
			slog.String("status", status),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", tid),
		}
		if session != "" {
			attrs = append(attrs, slog.String("session", session))
		}
		if resource != "" {
			attrs = append(attrs, slog.String("resource", resource))
		}
		if errMsg != "" {
			attrs = append(attrs, slog.String("error", errMsg))
		}

		if status == "error" {
			slog.LogAttrs(ctx, slog.LevelError, "mcp call completed", attrs...)
		} else {
			slog.LogAttrs(ctx, slog.LevelInfo, "mcp call completed", attrs...)
		}

		return result, err
	}
}

// extractErrorText extracts the text content from an error CallToolResult.
func extractErrorText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "unknown error"
}
