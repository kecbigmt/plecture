package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		health  *HealthConfig
		wantErr bool
	}{
		{name: "nil is valid (no probes declared)", health: nil},
		{name: "alive only", health: &HealthConfig{Alive: "kill -0 1"}},
		{name: "activity only", health: &HealthConfig{Activity: "probe"}},
		{name: "both", health: &HealthConfig{Alive: "kill -0 1", Activity: "probe"}},
		{name: "bare table declares nothing", health: &HealthConfig{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.health.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "[health]") {
				t.Fatalf("err = %q, want it to name the [health] table", err.Error())
			}
		})
	}
}

func writeHealthTaskFile(t *testing.T, body string) *Config {
	t.Helper()
	baseDir := t.TempDir()
	tasksDir := filepath.Join(baseDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tasksDir, "runtime.toml"), body)
	return &Config{BaseDir: baseDir}
}

func TestLoadTaskDefinitions_HealthProbesAreIndependent(t *testing.T) {
	tests := []struct {
		name         string
		table        string
		wantAlive    string
		wantActivity string
	}{
		{
			name:      "alive only",
			table:     "[health]\nalive = \"kill -0 {{.Self.pid}}\"\n",
			wantAlive: "kill -0 {{.Self.pid}}",
		},
		{
			name:         "activity only",
			table:        "[health]\nactivity = \"agent-activity probe\"\n",
			wantActivity: "agent-activity probe",
		},
		{
			name:         "both",
			table:        "[health]\nalive = \"kill -0 1\"\nactivity = \"agent-activity probe\"\n",
			wantAlive:    "kill -0 1",
			wantActivity: "agent-activity probe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := writeHealthTaskFile(t, "scope = \"run\"\nsetup = \"echo '{}'\"\n\n"+tt.table)
			defs, err := cfg.LoadTaskDefinitions("")
			if err != nil {
				t.Fatalf("LoadTaskDefinitions: %v", err)
			}
			got := defs["runtime"].Health
			if got.AliveProbe() != tt.wantAlive {
				t.Errorf("alive = %q, want %q", got.AliveProbe(), tt.wantAlive)
			}
			if got.ActivityProbe() != tt.wantActivity {
				t.Errorf("activity = %q, want %q", got.ActivityProbe(), tt.wantActivity)
			}
		})
	}
}

func TestLoadTaskDefinitions_HealthUnknownFieldRejected(t *testing.T) {
	cfg := writeHealthTaskFile(t, `
scope = "run"
setup = "echo '{}'"

[health]
alive     = "true"
readiness = "true"
`)
	_, err := cfg.LoadTaskDefinitions("")
	if err == nil {
		t.Fatal("expected an error for an unknown [health] field")
	}
	if !strings.Contains(err.Error(), "readiness") {
		t.Errorf("error %q should name the unknown field", err.Error())
	}
}

func TestLoadTaskDefinitions_HealthBareTableRejected(t *testing.T) {
	cfg := writeHealthTaskFile(t, "scope = \"run\"\nsetup = \"echo '{}'\"\n\n[health]\n")
	_, err := cfg.LoadTaskDefinitions("")
	if err == nil {
		t.Fatal("expected an error for a [health] table declaring no probe")
	}
	if !strings.Contains(err.Error(), "alive") || !strings.Contains(err.Error(), "activity") {
		t.Errorf("error %q should name both probes", err.Error())
	}
}

// A task file left un-migrated must fail loudly. Both retired keys are plain
// strings the TOML decoder drops in silence, so the alternative is a task
// that loads with no probe at all and a session that reads `undeclared` when
// its runtime has actually died.
func TestLoadTaskDefinitions_RetiredScalarsRejected(t *testing.T) {
	tests := []struct {
		key       string
		wantHints []string
	}{
		{key: "healthcheck", wantHints: []string{"[health]", "alive"}},
		{key: "movement_signal", wantHints: []string{"[health]", "activity"}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			cfg := writeHealthTaskFile(t, "scope = \"run\"\nsetup = \"echo '{}'\"\n"+tt.key+" = \"true\"\n")
			_, err := cfg.LoadTaskDefinitions("")
			if err == nil {
				t.Fatalf("expected an error for the retired %q key", tt.key)
			}
			for _, hint := range tt.wantHints {
				if !strings.Contains(err.Error(), hint) {
					t.Errorf("error %q should point at %q", err.Error(), hint)
				}
			}
		})
	}
}

func TestLoadWorkflows_RetiredTickMovementSourceRejected(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "workflows", "default.toml"), `
[[nodes]]
id   = "initial"
uses = "runtime"

[tick.movement_source]
name   = "fingerprint"
script = "echo hi"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadWorkflows("")
	if err == nil {
		t.Fatal("expected an error for the retired [tick.movement_source] table")
	}
	if !strings.Contains(err.Error(), "movement_source") || !strings.Contains(err.Error(), "[health].activity") {
		t.Errorf("error %q should name the retired table and its replacement", err.Error())
	}
}
