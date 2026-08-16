package busservice

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRunProcess_ForwardsChildOutputToLoggerWithServiceID(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "logger", `
echo "stdout marker"
echo "stderr marker" >&2
`)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	decl := Declaration{ID: "p/svc", PluginID: "p", Name: "svc", ExecPath: script}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := runProcess(ctx, decl, logger, time.Second, func(int) {}); err != nil {
		t.Fatalf("runProcess: unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "stdout marker") || !strings.Contains(out, `service=p/svc`) {
		t.Fatalf("log output missing stdout line tagged with service id, got:\n%s", out)
	}
	if !strings.Contains(out, "stderr marker") {
		t.Fatalf("log output missing stderr line, got:\n%s", out)
	}
	if !strings.Contains(out, "stream=stdout") || !strings.Contains(out, "stream=stderr") {
		t.Fatalf("log output missing stream tags, got:\n%s", out)
	}
}

func TestRunProcess_FlushesTrailingLineWithoutNewline(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "no-newline", `printf 'no trailing newline'`)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	decl := Declaration{ID: "p/svc2", PluginID: "p", Name: "svc2", ExecPath: script}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := runProcess(ctx, decl, logger, time.Second, func(int) {}); err != nil {
		t.Fatalf("runProcess: unexpected error: %v", err)
	}

	if !strings.Contains(buf.String(), "no trailing newline") {
		t.Fatalf("log output missing the unterminated trailing line, got:\n%s", buf.String())
	}
}
