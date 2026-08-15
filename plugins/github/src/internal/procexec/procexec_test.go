package procexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHostRun_Success(t *testing.T) {
	stdout, _, err := Host{}.Run(context.Background(), "", false, "echo", "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.TrimSpace(string(stdout)) != "hello" {
		t.Errorf("stdout = %q, want %q", stdout, "hello")
	}
}

func TestHostRun_StderrPreservedOnFailure(t *testing.T) {
	_, stderr, err := Host{}.Run(context.Background(), "", false, "sh", "-c", "echo failmsg 1>&2; exit 1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.TrimSpace(string(stderr)) != "failmsg" {
		t.Errorf("stderr = %q, want %q", stderr, "failmsg")
	}
}

func TestHostRun_ContextCancelKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		_, _, err = Host{}.Run(ctx, "", false, "sleep", "30")
		close(done)
	}()

	// Give the process a moment to actually start before cancelling.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return promptly after context cancellation; child process was not killed")
	}
	if err == nil {
		t.Error("expected error after context cancellation, got nil")
	}
}

func TestHostRun_ContextTimeoutKillsProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := Host{}.Run(ctx, "", false, "sleep", "30")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after context deadline exceeded, got nil")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run() took %v, expected it to return shortly after the 100ms deadline", elapsed)
	}
}
