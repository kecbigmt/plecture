package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// These tests cover cancellation for the five task exec paths (setup,
// cleanup, healthcheck, capture, dynamic-output fetch): each accepts a
// context.Context, and cancelling it must terminate the child process (so
// the marker file it would otherwise write never appears) and the error
// must surface to the caller promptly instead of the call blocking for the
// child's full lifetime.

const cancellationCharSleep = 150 * time.Millisecond

func TestCharacterization_RunSetup_CancelledContextKillsHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "sleep 5; touch '" + marker + "'; echo '{}'"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{}
	goCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunSetup(goCtx, plan.Run, SessionVars{Name: "x", WorkspaceDirPath: dir}, tasks, nil)
	if err == nil {
		t.Fatalf("RunSetup: want an error surfaced from the cancelled context, got nil")
	}
	if elapsed := time.Since(start); elapsed >= cancellationCharSleep {
		t.Errorf("RunSetup took %v, want it to return promptly once the context is cancelled", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("marker file exists: the child ran to completion despite the context being cancelled")
	}
}

func TestCharacterization_RunCleanup_CancelledContextKillsHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "echo '{}'", cleanup: "sleep 5; touch '" + marker + "'"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	goCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunCleanup(goCtx, plan.Run, SessionVars{Name: "x", WorkspaceDirPath: dir}, tasks, nil)
	if err == nil {
		t.Fatalf("RunCleanup: want an error surfaced from the cancelled context, got nil")
	}
	if elapsed := time.Since(start); elapsed >= cancellationCharSleep {
		t.Errorf("RunCleanup took %v, want it to return promptly once the context is cancelled", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("marker file exists: the child ran to completion despite the context being cancelled")
	}
}

func TestCharacterization_RunHealthcheck_CancelledContextKillsHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	session := SessionVars{Name: "x", WorkspaceDirPath: dir}
	goCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunHealthcheck(goCtx, "sleep 5; touch '"+marker+"'", map[string]any{}, map[string]any{}, session)
	if err == nil {
		t.Fatalf("RunHealthcheck: want an error surfaced from the cancelled context, got nil")
	}
	if elapsed := time.Since(start); elapsed >= cancellationCharSleep {
		t.Errorf("RunHealthcheck took %v, want it to return promptly once the context is cancelled", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("marker file exists: the child ran to completion despite the context being cancelled")
	}
}

func TestCharacterization_RunCapture_CancelledContextKillsHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	session := SessionVars{Name: "x", WorkspaceDirPath: dir}
	goCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunCapture(goCtx, "sleep 5; touch '"+marker+"'; echo done", map[string]any{}, session)
	if err == nil {
		t.Fatalf("RunCapture: want an error surfaced from the cancelled context, got nil")
	}
	if elapsed := time.Since(start); elapsed >= cancellationCharSleep {
		t.Errorf("RunCapture took %v, want it to return promptly once the context is cancelled", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("marker file exists: the child ran to completion despite the context being cancelled")
	}
}

func TestCharacterization_FetchOutput_CancelledContextKillsHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	cfg := &config.Config{}
	src := config.DynamicOutput{Name: "count", Script: "sleep 5; touch '" + marker + "'; echo 42"}
	renderCtx := RenderContext{Session: SessionVars{Name: "x", WorkspaceDirPath: dir}}
	goCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := FetchOutput(goCtx, cfg, src, renderCtx)
	if err == nil {
		t.Fatalf("FetchOutput: want an error surfaced from the cancelled context, got nil")
	}
	if elapsed := time.Since(start); elapsed >= cancellationCharSleep {
		t.Errorf("FetchOutput took %v, want it to return promptly once the context is cancelled", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Errorf("marker file exists: the child ran to completion despite the context being cancelled")
	}
}
