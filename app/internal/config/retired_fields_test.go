package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The effect grammar is closed, so a field outside it names itself in the
// load error rather than being read as a declaration with no consumer. A
// declaration left un-migrated fails on its missing `kind` before any field
// is examined at all, which is why the rows below carry one.
func TestLoadTaskDefinitions_FieldOutsideTheSurfaceRejected(t *testing.T) {
	for _, field := range []string{"primary", "idle_after", "execution"} {
		t.Run(field, func(t *testing.T) {
			base := t.TempDir()
			writeTaskFile(t, base, "work", "[work]\nkind = \"effect\"\nscope = \"session\"\n"+field+" = \"x\"\n"+`
[work.setup]
type   = "shell"
script = "true"
`)
			_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
			if err == nil {
				t.Fatalf("expected a load error naming the %s field", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error does not name the field: %v", err)
			}
		})
	}
}

func TestLoadWorkflows_RetiredEnvironmentFieldsRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "environment", body: "workspace_provider = \"github\"\nenvironment = \"docker\"\n", want: "`environment`"},
		{name: "environment_inputs", body: "workspace_provider = \"github\"\n[environment_inputs]\nimage = \"x\"\n", want: "`environment_inputs`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "workflows", "dev.toml"), tt.body)
			_, err := (&Config{BaseDir: base}).LoadWorkflows("")
			if err == nil {
				t.Fatalf("expected a load error naming the retired %s field", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not name the retired field: %v", err)
			}
		})
	}
}
