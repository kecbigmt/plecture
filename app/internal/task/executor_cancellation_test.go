package task

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// These tests characterize today's behavior for the five task exec paths
// (setup, cleanup, healthcheck, capture, dynamic-output fetch) when the
// underlying child process takes a while to finish: none of these entry
// points accept anything resembling a cancellation signal, so a caller that
// wants to give up early has no way to do so — the call always blocks until
// the child process exits on its own and the child always runs to
// completion. Each subtest runs a script that sleeps briefly and then writes
// a marker file, and asserts the marker exists once the call returns,
// pinning down that the child is never interrupted mid-flight.

const cancellationCharSleep = 150 * time.Millisecond

func TestCharacterization_RunSetup_NoWayToCancelHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "sleep 0.15; touch '" + marker + "'; echo '{}'"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{}
	start := time.Now()
	if err := RunSetup(plan.Run, SessionVars{Name: "x", WorktreePath: dir}, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if elapsed := time.Since(start); elapsed < cancellationCharSleep {
		t.Errorf("RunSetup returned after %v, want it to block for the full child lifetime (>= %v)", elapsed, cancellationCharSleep)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: the child ran to completion despite no cancellation signal ever being possible: %v", err)
	}
}

func TestCharacterization_RunCleanup_NoWayToCancelHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	plan := buildPlan(t,
		[]taskStub{{id: "a", scope: "run", setup: "echo '{}'", cleanup: "sleep 0.15; touch '" + marker + "'"}},
		[]nodeStub{{id: "a"}},
	)
	tasks := map[string]*contract.TaskState{
		"a": {Scope: "run", Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	}
	start := time.Now()
	if err := RunCleanup(plan.Run, SessionVars{Name: "x", WorktreePath: dir}, tasks, nil); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	if elapsed := time.Since(start); elapsed < cancellationCharSleep {
		t.Errorf("RunCleanup returned after %v, want it to block for the full child lifetime (>= %v)", elapsed, cancellationCharSleep)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: the child ran to completion despite no cancellation signal ever being possible: %v", err)
	}
}

func TestCharacterization_RunHealthcheck_NoWayToCancelHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	session := SessionVars{Name: "x", WorktreePath: dir}
	start := time.Now()
	if err := RunHealthcheck("sleep 0.15; touch '"+marker+"'", map[string]any{}, map[string]any{}, session); err != nil {
		t.Fatalf("RunHealthcheck: %v", err)
	}
	if elapsed := time.Since(start); elapsed < cancellationCharSleep {
		t.Errorf("RunHealthcheck returned after %v, want it to block for the full child lifetime (>= %v)", elapsed, cancellationCharSleep)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: the child ran to completion despite no cancellation signal ever being possible: %v", err)
	}
}

func TestCharacterization_RunCapture_NoWayToCancelHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	session := SessionVars{Name: "x", WorktreePath: dir}
	start := time.Now()
	if _, err := RunCapture("sleep 0.15; touch '"+marker+"'; echo done", map[string]any{}, session); err != nil {
		t.Fatalf("RunCapture: %v", err)
	}
	if elapsed := time.Since(start); elapsed < cancellationCharSleep {
		t.Errorf("RunCapture returned after %v, want it to block for the full child lifetime (>= %v)", elapsed, cancellationCharSleep)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: the child ran to completion despite no cancellation signal ever being possible: %v", err)
	}
}

func TestCharacterization_FetchOutput_NoWayToCancelHungChild(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	cfg := &config.Config{}
	src := config.DynamicOutput{Name: "count", Script: "sleep 0.15; touch '" + marker + "'; echo 42"}
	ctx := RenderContext{Session: SessionVars{Name: "x", WorktreePath: dir}}
	start := time.Now()
	if _, err := FetchOutput(cfg, src, ctx); err != nil {
		t.Fatalf("FetchOutput: %v", err)
	}
	if elapsed := time.Since(start); elapsed < cancellationCharSleep {
		t.Errorf("FetchOutput returned after %v, want it to block for the full child lifetime (>= %v)", elapsed, cancellationCharSleep)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: the child ran to completion despite no cancellation signal ever being possible: %v", err)
	}
}
