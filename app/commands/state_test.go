package commands

import (
	"strings"
	"testing"
)

func TestSetOutputHelpDocumentsRuntimeTaskTarget(t *testing.T) {
	if !strings.Contains(setOutputCmd.Long, "--task <task-handle>") {
		t.Fatalf("set-output long help must document --task <task-handle>; got:\n%s", setOutputCmd.Long)
	}
	flag := setOutputCmd.Flags().Lookup("task")
	if flag == nil {
		t.Fatal("set-output missing --task flag")
	}
	if flag.Usage != "Target a produced runtime task such as review#1" {
		t.Fatalf("--task usage = %q", flag.Usage)
	}
	if !strings.Contains(setOutputCmd.Long, "plect state set-output workspace-1 --task review#1") {
		t.Fatalf("set-output examples must show --task review#1; got:\n%s", setOutputCmd.Long)
	}
}
