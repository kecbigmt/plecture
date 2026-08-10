package task

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunShell_CapturesStdoutAndStderrSeparately(t *testing.T) {
	// stdout flows into the JSON outputs parser; stderr is captured so the
	// reporter can dump it after the spinner stops. They must not bleed into
	// each other.
	stdout, stderr, err := runShell(`echo out; echo err >&2`, "")
	if err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if string(stdout) != "out\n" {
		t.Errorf("stdout = %q, want \"out\\n\"", stdout)
	}
	if string(stderr) != "err\n" {
		t.Errorf("stderr = %q, want \"err\\n\"", stderr)
	}
}

func TestRunShell_CapturesStderrOnFailure(t *testing.T) {
	stdout, stderr, err := runShell(`echo before-fail; echo diagnostic >&2; exit 7`, "")
	if err == nil {
		t.Fatalf("expected non-nil err for non-zero exit, got nil")
	}
	if string(stdout) != "before-fail\n" {
		t.Errorf("stdout = %q, want \"before-fail\\n\"", stdout)
	}
	if string(stderr) != "diagnostic\n" {
		t.Errorf("stderr = %q, want \"diagnostic\\n\"", stderr)
	}
}

// bytes.Buffer is not *os.File, so writerIsTerminal returns false and the
// observer runs in non-TTY mode — exactly the behaviour we want for tests.

func TestStreamReporter_NonTTY_Success(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"tmux"})

	r.OnStart("run", "tmux")
	r.OnSuccess("run", "tmux", 23*time.Millisecond, nil)

	out := buf.String()
	if !strings.HasPrefix(out, "run:\n") {
		t.Fatalf("expected scope header 'run:' on the first line, got %q", out)
	}
	if !strings.Contains(out, "✓") {
		t.Fatalf("expected success icon, got %q", out)
	}
	if !strings.Contains(out, "23ms") {
		t.Fatalf("expected elapsed 23ms, got %q", out)
	}
	if !strings.Contains(out, "tmux") {
		t.Fatalf("expected id 'tmux', got %q", out)
	}
}

func TestStreamReporter_DumpsStderrOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"teardown"})

	r.OnStart("run", "teardown")
	r.OnSuccess("run", "teardown", time.Millisecond, []byte("direnv: loading\nWARNING: leftover pid=123\n"))

	out := buf.String()
	// Status line still appears.
	if !strings.Contains(out, "✓") {
		t.Fatalf("expected success icon, got %q", out)
	}
	// Each stderr line is indented under the status line.
	if !strings.Contains(out, "\n      direnv: loading\n") {
		t.Fatalf("expected indented stderr line, got %q", out)
	}
	if !strings.Contains(out, "\n      WARNING: leftover pid=123\n") {
		t.Fatalf("expected indented WARNING line, got %q", out)
	}
	// Status line must precede the dump.
	statusIdx := strings.Index(out, "✓")
	dumpIdx := strings.Index(out, "direnv: loading")
	if statusIdx < 0 || dumpIdx < 0 || statusIdx > dumpIdx {
		t.Fatalf("status line should precede stderr dump, got %q", out)
	}
}

func TestStreamReporter_DumpsStderrOnFailure(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"setup"})

	r.OnStart("run", "setup")
	r.OnFailure("run", "setup", time.Millisecond, errors.New("exit 1"), []byte("ERROR: something broke\n"))

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Fatalf("expected failure icon, got %q", out)
	}
	if !strings.Contains(out, "\n      ERROR: something broke\n") {
		t.Fatalf("expected indented stderr dump under failure, got %q", out)
	}
}

func TestStreamReporter_SkipsEmptyStderr(t *testing.T) {
	for _, stderr := range [][]byte{nil, {}, []byte("   \n\n\t  ")} {
		var buf bytes.Buffer
		r := NewStreamReporter(&buf, []string{"tmux"})
		r.OnStart("run", "tmux")
		r.OnSuccess("run", "tmux", time.Millisecond, stderr)
		out := buf.String()
		// No indented dump lines — only the header + status line should appear.
		if strings.Contains(out, "\n      ") {
			t.Fatalf("expected no indented dump for empty stderr (%q), got %q", stderr, out)
		}
	}
}

func TestStreamReporter_StderrTrailingPartialLine(t *testing.T) {
	// Last line of captured stderr may be missing its trailing newline (e.g.
	// `printf "WARNING: ..."` with no \n). The dump must still surface it.
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"a"})
	r.OnStart("run", "a")
	r.OnSuccess("run", "a", time.Millisecond, []byte("first\nlast-without-newline"))
	out := buf.String()
	if !strings.Contains(out, "\n      first\n") {
		t.Fatalf("expected first stderr line, got %q", out)
	}
	if !strings.Contains(out, "\n      last-without-newline\n") {
		t.Fatalf("expected last (newline-less) line to still surface, got %q", out)
	}
}

func TestStreamReporter_NonTTY_Failure(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"a"})

	r.OnStart("run", "a")
	r.OnFailure("run", "a", 100*time.Millisecond, errors.New("boom"), nil)

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Fatalf("expected failure icon, got %q", out)
	}
	// Error text is intentionally omitted; cobra (or CleanupWarnings for
	// --force destroy) prints the reason exactly once.
	if strings.Contains(out, "boom") {
		t.Fatalf("error message should not appear in progress line, got %q", out)
	}
}

func TestStreamReporter_NonTTY_Skip(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"slack-env"})

	r.OnSkip("run", "slack-env", "already produced")

	out := buf.String()
	if !strings.Contains(out, "⊘") {
		t.Fatalf("expected skip icon, got %q", out)
	}
	if !strings.Contains(out, "skipped (already produced)") {
		t.Fatalf("expected reason annotation, got %q", out)
	}
}

func TestStreamReporter_NonTTY_StaysSilentDuringRun(t *testing.T) {
	// In non-TTY mode the only output during OnStart is the scope header
	// (emitted once per scope transition). The id line itself must wait
	// for the terminal event so piping doesn't print half lines.
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, nil)

	r.OnStart("run", "slow")
	got := buf.String()
	if got != "run:\n" {
		t.Fatalf("non-TTY OnStart should emit only the scope header, got %q", got)
	}
	r.OnSuccess("run", "slow", time.Millisecond, nil)
}

func TestStreamReporter_ScopeHeaderEmittedOnce(t *testing.T) {
	// Two events in the same scope should produce a single `scope:` line.
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"a", "b"})

	r.OnSuccess("run", "a", time.Millisecond, nil)
	r.OnSuccess("run", "b", time.Millisecond, nil)

	if n := strings.Count(buf.String(), "run:\n"); n != 1 {
		t.Fatalf("expected exactly one 'run:' header, got %d in %q", n, buf.String())
	}
}

func TestStreamReporter_ScopeHeaderEmittedOnChange(t *testing.T) {
	var buf bytes.Buffer
	r := NewStreamReporter(&buf, []string{"envfile", "tmux"})

	r.OnSuccess("session", "envfile", time.Millisecond, nil)
	r.OnSuccess("run", "tmux", time.Millisecond, nil)

	out := buf.String()
	if !strings.Contains(out, "session:\n") || !strings.Contains(out, "run:\n") {
		t.Fatalf("expected both scope headers, got %q", out)
	}
	if strings.Index(out, "session:") > strings.Index(out, "run:") {
		t.Fatalf("expected session: before run:, got %q", out)
	}
}

func TestStreamReporter_PadsID(t *testing.T) {
	var buf bytes.Buffer
	// Two ids of different widths — both lines should align on the id column.
	r := NewStreamReporter(&buf, []string{"a", "longer-id"})

	r.OnSuccess("run", "a", time.Millisecond, nil)
	r.OnSuccess("run", "longer-id", time.Millisecond, nil)

	// Skip the scope header line; only compare task lines.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 entries), got %d: %q", len(lines), buf.String())
	}
	pos0 := strings.Index(lines[1], "✓")
	pos1 := strings.Index(lines[2], "✓")
	if pos0 == -1 || pos1 == -1 {
		t.Fatalf("missing ✓ in lines: %v", lines)
	}
	if pos0 != pos1 {
		t.Fatalf("alignment broken: ✓ at %d and %d in %q", pos0, pos1, buf.String())
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{500 * time.Microsecond, "500µs"},
		{12 * time.Millisecond, "12ms"},
		{1500 * time.Millisecond, "1.50s"},
		{10500 * time.Millisecond, "10.5s"},
		{2 * time.Minute, "2m0s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNilObserver_NoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil observer caused panic: %v", r)
		}
	}()
	observerOr(nil).OnStart("run", "x")
	observerOr(nil).OnSuccess("run", "x", time.Millisecond, nil)
	observerOr(nil).OnFailure("run", "x", time.Millisecond, errors.New("e"), nil)
	observerOr(nil).OnSkip("run", "x", "reason")
}
