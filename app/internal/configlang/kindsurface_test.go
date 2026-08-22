package configlang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestKindSurfaceRejectsAForeignField(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a lifecycle field on a task document",
			src: `+++
[work]
kind              = "task"
resource_observer = "issue_pr"

[work.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]
+++
Resolve the issue.
`,
		},
		{
			name: "a completion contract on an effect",
			src: `[render]
kind = "effect"

[render.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
`,
		},
		{
			name: "chains on an effect",
			src: `[render]
kind = "effect"

[[render.chains]]
id       = "review"
workflow = "review_session"
`,
		},
		{
			name: "an observer reaching for a lifecycle",
			src: `[goal]
kind = "resource_observer"

[goal.cleanup]
type = "exec"
bin  = "okf-goal"
args = ["forget"]
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSource(t, tc.src)
			wantDiag(t, err, CodeFieldUnknown, LayerStructural)
		})
	}
}

func TestKindSurfaceAcceptsEveryDeclaredField(t *testing.T) {
	src := `[goal]
kind  = "resource_observer"
match = '^local-okf://'

[goal.observe]
type = "exec"
bin  = "okf-goal"
args = ["observe"]

[goal.state_schema]
type = "object"
`
	if err := validateSource(t, src); err != nil {
		t.Fatalf("a definition using only its kind's fields must load: %v", err)
	}
}

// TestKindSurfaceMatchesSchema pins the per-kind field sets to
// plecture.schema.json, which is authoritative for structural shape: a field
// added to one and not the other would otherwise be accepted by the schema
// and rejected by this package, or the reverse.
func TestKindSurfaceMatchesSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "plecture.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for kind, def := range map[Kind]string{
		KindEffect:            "effectDefinition",
		KindTask:              "taskDefinition",
		KindChannel:           "channelDefinition",
		KindWorkflow:          "workflowDefinition",
		KindWorkspaceProvider: "workspaceProviderDefinition",
		KindResourceObserver:  "resourceObserverDefinition",
	} {
		t.Run(string(kind), func(t *testing.T) {
			var want []string
			for field := range doc.Defs[def].Properties {
				if field == "kind" {
					continue // parseDefinitionTable lifts kind out of Body
				}
				want = append(want, field)
			}
			sort.Strings(want)
			var got []string
			for field := range kindFields[kind] {
				got = append(got, field)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("kindFields[%s] = %v, schema %s declares %v", kind, got, def, want)
			}
		})
	}
}
