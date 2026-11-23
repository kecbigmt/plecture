package lang

import (
	"strings"
	"testing"
)

func TestRetiredTaskBudgetIsALoadErrorThatPointsAtTheMigrationGuide(t *testing.T) {
	body := `
[work]
kind         = "task"
resource     = "issue_pr"
instructions = [{ text = "Resolve it." }]

[work.done_when]
all = [{ judge = "done", id = "done" }]

[work.budget]
heartbeat_budget = 3
`
	defs, err := ParseDefinitionDocument("f.toml", []byte(body))
	if err == nil {
		for _, def := range defs {
			if err = (Validation{}).ValidateDefinition(def); err != nil {
				break
			}
		}
	}
	assertDiagnostic(t, err, CodeFieldUnknown, LayerStructural)
	if !strings.Contains(err.Error(), HeartbeatBudgetMigrationGuide) {
		t.Errorf("error %q does not point at the migration guide", err)
	}
}
