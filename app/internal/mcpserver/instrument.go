package mcpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kecbigmt/sennit/app/internal/github"
	"github.com/kecbigmt/sennit/app/internal/traceid"
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

		// Derive session name and url from either the "url" or "session"
		// argument. New lifecycle tools (sennit_up/down/destroy) use "session"
		// — accept either a URL or a bare session name there.
		session := ""
		urlParam := request.GetString("url", "")
		sessionParam := request.GetString("session", "")
		switch {
		case urlParam != "":
			if parsed, parseErr := github.ParseURL(urlParam); parseErr == nil {
				session = github.SessionName(parsed.OwnerRepo, parsed.Number)
			}
		case sessionParam != "":
			if github.IsURL(sessionParam) {
				if parsed, parseErr := github.ParseURL(sessionParam); parseErr == nil {
					session = github.SessionName(parsed.OwnerRepo, parsed.Number)
					urlParam = sessionParam
				}
			} else {
				session = sessionParam
			}
		}

		// Build slog attributes.
		attrs := []slog.Attr{
			slog.String("component", "sennit-mcp"),
			slog.String("event", "mcp_call"),
			slog.String("tool", toolName),
			slog.String("status", status),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", tid),
		}
		if session != "" {
			attrs = append(attrs, slog.String("session", session))
		}
		if urlParam != "" {
			attrs = append(attrs, slog.String("url", urlParam))
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
