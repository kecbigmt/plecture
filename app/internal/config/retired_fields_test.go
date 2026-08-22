package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A retired key the decoder would otherwise drop silently must name itself in
// the load error: a task that still declares `primary` or `execution` would
// otherwise load clean and leave the author believing the declaration is live.
func TestLoadTaskDefinitions_RetiredFieldsRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "primary", body: "scope = \"session\"\nsetup = \"true\"\nprimary = true\n", want: "`primary`"},
		{name: "idle_after", body: "scope = \"session\"\nsetup = \"true\"\nidle_after = \"30m\"\n", want: "`idle_after`"},
		{name: "execution", body: "scope = \"session\"\nsetup = \"true\"\nexecution = \"environment\"\n", want: "`execution`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			writeTaskFile(t, base, "work", tt.body)
			_, err := (&Config{BaseDir: base}).LoadTaskDefinitions("")
			if err == nil {
				t.Fatalf("expected a load error naming the retired %s field", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not name the retired field: %v", err)
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
