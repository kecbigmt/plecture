package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// captureSlog replaces the default slog logger with one that writes JSON to buf,
// and returns a cleanup function to restore the original.
func captureSlog(buf *bytes.Buffer) func() {
	orig := slog.Default()
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	return func() { slog.SetDefault(orig) }
}

func TestInstrumentHandler_Success(t *testing.T) {
	var slogBuf bytes.Buffer
	cleanup := captureSlog(&slogBuf)
	defer cleanup()

	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Verify trace_id is available in context.
		tid := traceIDFromContext(ctx)
		if tid == "" {
			t.Error("expected trace_id in context")
		}
		return mcp.NewToolResultText(`{"ok":true}`), nil
	}

	handler := instrumentHandler("sennit_list", inner)
	result, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}

	// Parse slog output.
	rec := parseSlogRecord(t, slogBuf.String())
	assertField(t, rec, "component", "sennit-mcp")
	assertField(t, rec, "tool", "sennit_list")
	assertField(t, rec, "status", "ok")
	assertField(t, rec, "level", "INFO")
	assertFieldPresent(t, rec, "trace_id")
	assertFieldPresent(t, rec, "duration_ms")
}

func TestInstrumentHandler_ToolError(t *testing.T) {
	var slogBuf bytes.Buffer
	cleanup := captureSlog(&slogBuf)
	defer cleanup()

	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("session already exists"), nil
	}

	handler := instrumentHandler("sennit_up", inner)

	// Build a request with url parameter.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": "https://github.com/acme/widgets/issues/170",
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}

	// Check slog.
	rec := parseSlogRecord(t, slogBuf.String())
	assertField(t, rec, "component", "sennit-mcp")
	assertField(t, rec, "event", "mcp_call")
	assertField(t, rec, "tool", "sennit_up")
	assertField(t, rec, "status", "error")
	assertField(t, rec, "level", "ERROR")
	assertField(t, rec, "error", "session already exists")
	// The resource identifier is logged verbatim; deriving a session name
	// from it is the provider resolver's job, not the log wrapper's.
	assertField(t, rec, "resource", "https://github.com/acme/widgets/issues/170")
	assertFieldPresent(t, rec, "trace_id")
	assertFieldPresent(t, rec, "duration_ms")
}

func TestInstrumentHandler_TraceIDContinuity(t *testing.T) {
	var slogBuf bytes.Buffer
	cleanup := captureSlog(&slogBuf)
	defer cleanup()

	var capturedTraceID string
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		capturedTraceID = traceIDFromContext(ctx)
		return mcp.NewToolResultError("fail"), nil
	}

	handler := instrumentHandler("sennit_destroy", inner)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": "https://github.com/acme/widgets/pull/42",
	}
	handler(context.Background(), req)

	// The trace_id in slog should match the one the handler received.
	if capturedTraceID == "" {
		t.Fatal("handler did not receive trace_id via context")
	}
	if !strings.HasPrefix(capturedTraceID, "tr_") {
		t.Errorf("trace_id should start with tr_, got %q", capturedTraceID)
	}

	rec := parseSlogRecord(t, slogBuf.String())
	if rec["trace_id"] != capturedTraceID {
		t.Errorf("slog trace_id = %v, want %v", rec["trace_id"], capturedTraceID)
	}
}

// --- helpers ---

func parseSlogRecord(t *testing.T, raw string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) == 0 {
		t.Fatal("no slog output")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("failed to parse slog JSON: %v\nraw: %s", err, raw)
	}
	return rec
}

func assertField(t *testing.T, rec map[string]any, key, want string) {
	t.Helper()
	got, ok := rec[key]
	if !ok {
		t.Errorf("missing field %q in slog record", key)
		return
	}
	if gotStr := got.(string); gotStr != want {
		t.Errorf("slog %s = %q, want %q", key, gotStr, want)
	}
}

func assertFieldPresent(t *testing.T, rec map[string]any, key string) {
	t.Helper()
	v, ok := rec[key]
	if !ok {
		t.Errorf("missing field %q in slog record", key)
		return
	}
	if v == nil || v == "" {
		t.Errorf("field %q is empty", key)
	}
}
