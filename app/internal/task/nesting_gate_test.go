package task

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// TestResolveDefinition_NestedTaskIsNotYetExecutable pins the gate that keeps
// a parsed-but-unimplemented nesting declaration from compiling into a plan
// that would run the outermost layer alone and drop the joint in silence.
func TestResolveDefinition_NestedTaskIsNotYetExecutable(t *testing.T) {
	def := config.TaskDefinition{ID: "outer", Scope: "run", Setup: "true", Inner: "claude"}
	_, err := ResolveDefinition(def, "outer")
	if err == nil {
		t.Fatal("ResolveDefinition: want an error for a nested definition, got nil")
	}
	if !strings.Contains(err.Error(), "not yet executable") {
		t.Errorf("error = %v, want it to say nesting is not executable yet", err)
	}
}
