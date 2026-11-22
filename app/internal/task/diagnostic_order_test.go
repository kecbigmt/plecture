package task

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

func TestCompileWorkflow_ReportsTheFirstUnknownNodePublicOutputInKeyOrder(t *testing.T) {
	wf, defs := publicOutputWorkflow()
	wf.Outputs = map[string]*lang.Value{
		"c": fromValue("nodes.ghost_c.outputs.x"),
		"a": fromValue("nodes.ghost_a.outputs.x"),
		"b": fromValue("nodes.ghost_b.outputs.x"),
	}
	wf.Nodes[0].Inputs = nil
	for i := range 50 {
		_, err := CompileWorkflow(wf, defs)
		if err == nil || !strings.Contains(err.Error(), `public output "a"`) {
			t.Fatalf("compile %d: want the unknown-node error for public output a, got %v", i, err)
		}
	}
}
