package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
)

func TestRun_ExecutesCommand(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.txt")

	hooks := []config.HookConfig{
		{Command: "echo hello > " + outFile},
	}
	vars := Vars{
		SessionName:  "test-session",
		WorktreePath: tmpDir,
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	got := string(data)
	if got != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", got)
	}
}

func TestRun_StdinJSONPassed(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.json")

	hooks := []config.HookConfig{
		{Command: "cat > " + outFile},
	}
	vars := Vars{
		SessionName:  "my-session",
		WorktreePath: tmpDir,
		URL:          "https://github.com/foo/bar",
		OwnerRepo:    "foo/bar",
		Branch:       "feat/test",
		HookArgs:     []string{"--flag", "value"},
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var got Vars
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to parse JSON from stdin: %v\nraw: %s", err, string(data))
	}

	if got.SessionName != "my-session" {
		t.Errorf("session_name = %q, want %q", got.SessionName, "my-session")
	}
	if got.URL != "https://github.com/foo/bar" {
		t.Errorf("url = %q, want %q", got.URL, "https://github.com/foo/bar")
	}
	if got.OwnerRepo != "foo/bar" {
		t.Errorf("owner_repo = %q, want %q", got.OwnerRepo, "foo/bar")
	}
	if got.Branch != "feat/test" {
		t.Errorf("branch = %q, want %q", got.Branch, "feat/test")
	}
	expectedArgs := []string{"--flag", "value"}
	if len(got.HookArgs) != len(expectedArgs) {
		t.Errorf("hook_args length = %d, want %d", len(got.HookArgs), len(expectedArgs))
	} else {
		for i, v := range expectedArgs {
			if got.HookArgs[i] != v {
				t.Errorf("hook_args[%d] = %q, want %q", i, got.HookArgs[i], v)
			}
		}
	}
}

func TestRun_StdinJSONWithSpecialChars(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.json")

	hooks := []config.HookConfig{
		{Command: "cat > " + outFile},
	}
	vars := Vars{
		SessionName:   "test-session",
		WorktreePath:  tmpDir,
		ChangeSummary: `it's a "test" with $pecial chars & newline` + "\n" + "second line",
		ChangeType:    "ci_status",
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var got Vars
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to parse JSON from stdin: %v\nraw: %s", err, string(data))
	}

	expected := `it's a "test" with $pecial chars & newline` + "\n" + "second line"
	if got.ChangeSummary != expected {
		t.Errorf("change_summary = %q, want %q", got.ChangeSummary, expected)
	}
}

func TestRun_StdinJSONReadWithJq(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.txt")

	hooks := []config.HookConfig{
		{Command: "jq -r '.session_name + \" \" + .url' > " + outFile},
	}
	vars := Vars{
		SessionName:  "my-session",
		WorktreePath: tmpDir,
		URL:          "https://github.com/foo/bar",
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	expected := "my-session https://github.com/foo/bar\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestRun_MultipleHooks(t *testing.T) {
	tmpDir := t.TempDir()
	outFile1 := filepath.Join(tmpDir, "out1.txt")
	outFile2 := filepath.Join(tmpDir, "out2.txt")

	hooks := []config.HookConfig{
		{Command: "echo first > " + outFile1},
		{Command: "echo second > " + outFile2},
	}
	vars := Vars{
		WorktreePath: tmpDir,
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data1, err := os.ReadFile(outFile1)
	if err != nil {
		t.Fatalf("failed to read out1: %v", err)
	}
	if string(data1) != "first\n" {
		t.Errorf("out1: expected %q, got %q", "first\n", string(data1))
	}

	data2, err := os.ReadFile(outFile2)
	if err != nil {
		t.Fatalf("failed to read out2: %v", err)
	}
	if string(data2) != "second\n" {
		t.Errorf("out2: expected %q, got %q", "second\n", string(data2))
	}
}

func TestRun_NoHooks(t *testing.T) {
	err := Run(PostSyncChange, nil, Vars{})
	if err != nil {
		t.Fatalf("Run with nil hooks returned error: %v", err)
	}

	err = Run(PostSyncChange, []config.HookConfig{}, Vars{})
	if err != nil {
		t.Fatalf("Run with empty hooks returned error: %v", err)
	}
}

func TestRun_HookFailure(t *testing.T) {
	tmpDir := t.TempDir()

	hooks := []config.HookConfig{
		{Command: "exit 1"},
	}
	vars := Vars{
		WorktreePath: tmpDir,
	}

	err := Run(PostSyncChange, hooks, vars)
	if err == nil {
		t.Fatal("expected error from failing hook, got nil")
	}
}

func TestRun_WorktreePathNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.txt")

	hooks := []config.HookConfig{
		{Command: "echo ok > " + outFile},
	}
	vars := Vars{
		SessionName:  "test-session",
		WorktreePath: "/nonexistent/path/that/does/not/exist",
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("expected hook to succeed when WorktreePath does not exist, got error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if string(data) != "ok\n" {
		t.Errorf("expected %q, got %q", "ok\n", string(data))
	}
}

func TestRun_TraceIDInStdin(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "out.json")

	hooks := []config.HookConfig{
		{Command: "cat > " + outFile},
	}
	vars := Vars{
		SessionName:  "test-session",
		WorktreePath: tmpDir,
		TraceID:      "tr_abcd1234",
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var got Vars
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to parse JSON from stdin: %v\nraw: %s", err, string(data))
	}

	if got.TraceID != "tr_abcd1234" {
		t.Errorf("trace_id = %q, want %q", got.TraceID, "tr_abcd1234")
	}
}

func TestRun_MultipleHooksEachGetsStdin(t *testing.T) {
	tmpDir := t.TempDir()
	outFile1 := filepath.Join(tmpDir, "out1.json")
	outFile2 := filepath.Join(tmpDir, "out2.json")

	hooks := []config.HookConfig{
		{Command: "cat > " + outFile1},
		{Command: "cat > " + outFile2},
	}
	vars := Vars{
		SessionName:  "test-session",
		WorktreePath: tmpDir,
		URL:          "https://github.com/foo/bar",
	}

	err := Run(PostSyncChange, hooks, vars)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Both hooks should receive the same JSON via stdin
	for i, outFile := range []string{outFile1, outFile2} {
		data, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("hook %d: failed to read output: %v", i, err)
		}
		var got Vars
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("hook %d: failed to parse JSON: %v\nraw: %s", i, err, string(data))
		}
		if got.SessionName != "test-session" {
			t.Errorf("hook %d: session_name = %q, want %q", i, got.SessionName, "test-session")
		}
	}
}
