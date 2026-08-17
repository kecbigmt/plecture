package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		term    *TerminalConfig
		wantErr bool
		wantMsg string // substring, only checked when wantErr
	}{
		{name: "nil is valid (no interactive endpoint declared)", term: nil},
		{name: "bare empty table is valid", term: &TerminalConfig{}},
		{
			name: "all four declared",
			term: &TerminalConfig{Attach: "a", Capture: "c", SendText: "t", SendKeys: "k"},
		},
		{
			name:    "only attach declared",
			term:    &TerminalConfig{Attach: "a"},
			wantErr: true,
			wantMsg: "capture, send_text, send_keys",
		},
		{
			name:    "missing only send_keys",
			term:    &TerminalConfig{Attach: "a", Capture: "c", SendText: "t"},
			wantErr: true,
			wantMsg: "send_keys",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.term.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

// TestLoadTaskDefinitions_TerminalAllFourAccepted guards the happy path: a
// task declaring every [terminal] member loads with Terminal populated.
func TestLoadTaskDefinitions_TerminalAllFourAccepted(t *testing.T) {
	baseDir := t.TempDir()
	tasksDir := filepath.Join(baseDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tasksDir, "tmux.toml"), `
scope = "run"
setup = "echo '{}'"

[terminal]
attach     = "tmux attach -t {{.Self.session_name}}"
capture    = "tmux capture-pane -p -t {{.Self.session_name}}"
send_text  = "tmux send-keys -t {{.Self.session_name}} -- \"$1\""
send_keys  = "tmux send-keys -t {{.Self.session_name}} \"$1\""
`)
	cfg := &Config{BaseDir: baseDir}
	defs, err := cfg.LoadTaskDefinitions("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def, ok := defs["tmux"]
	if !ok {
		t.Fatal("tmux task not loaded")
	}
	if def.Terminal == nil {
		t.Fatal("expected Terminal to be populated")
	}
	if def.Terminal.Attach == "" || def.Terminal.Capture == "" || def.Terminal.SendText == "" || def.Terminal.SendKeys == "" {
		t.Fatalf("expected all four terminal verbs to be populated, got %+v", def.Terminal)
	}
}

// TestTerminalConfigValidate_ConformsToCanonicalSchema is the schema-driven
// pilot's conformance check: for a representative sweep of [terminal] table
// shapes, TerminalConfig.Validate's accept/reject verdict must agree with
// the independently-authored schema/terminal.schema.json's, so the
// hand-written Go validation and the canonical schema document can't
// silently drift apart.
func TestTerminalConfigValidate_ConformsToCanonicalSchema(t *testing.T) {
	tests := []map[string]any{
		{},
		{"attach": "a", "capture": "c", "send_text": "t", "send_keys": "k"},
		{"attach": "a"},
		{"capture": "c"},
		{"send_text": "t"},
		{"send_keys": "k"},
		{"attach": "a", "capture": "c"},
		{"attach": "a", "capture": "c", "send_text": "t"},
	}
	for _, data := range tests {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			schemaErr := terminalSchema.Validate(data)
			loaderErr := decodeTerminalConfig(data).Validate()
			if (schemaErr == nil) != (loaderErr == nil) {
				t.Fatalf("schema valid=%v (err=%v) but loader valid=%v (err=%v) for %v",
					schemaErr == nil, schemaErr, loaderErr == nil, loaderErr, data)
			}
		})
	}
}

// decodeTerminalConfig mirrors what TOML decoding a [terminal] table
// produces: a nil pointer for an absent/empty table reads as "not declared"
// the same way Validate already treats a bare-empty pointer, and a present
// key becomes its matching field.
func decodeTerminalConfig(data map[string]any) *TerminalConfig {
	if len(data) == 0 {
		return nil
	}
	get := func(k string) string {
		v, _ := data[k].(string)
		return v
	}
	return &TerminalConfig{Attach: get("attach"), Capture: get("capture"), SendText: get("send_text"), SendKeys: get("send_keys")}
}

// TestLoadTaskDefinitions_TopLevelAttachRejected guards against the old
// top-level `attach`/`capture` keys silently vanishing (BurntSushi's decoder
// otherwise just ignores an unrecognized key) now that they live under
// [terminal] — a task file left un-migrated must fail loudly, not load with
// its attach target quietly dropped.
func TestLoadTaskDefinitions_TopLevelAttachRejected(t *testing.T) {
	for _, key := range []string{"attach", "capture"} {
		t.Run(key, func(t *testing.T) {
			baseDir := t.TempDir()
			tasksDir := filepath.Join(baseDir, "tasks")
			if err := os.MkdirAll(tasksDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(tasksDir, "tmux.toml"),
				"scope = \"run\"\nsetup = \"echo '{}'\"\n"+key+" = \"echo hi\"\n")
			cfg := &Config{BaseDir: baseDir}
			_, err := cfg.LoadTaskDefinitions("")
			if err == nil {
				t.Fatalf("expected an error for top-level %q", key)
			}
			if !strings.Contains(err.Error(), "[terminal]") {
				t.Errorf("error %q should point at [terminal]", err.Error())
			}
		})
	}
}

// TestLoadTaskDefinitions_TerminalUnknownFieldRejected guards the
// additionalProperties: false the canonical schema declares — a typo'd verb
// name must fail to load instead of silently vanishing as an unrecognized
// TOML key, mirroring TestLoadTaskDefinitions_TopLevelAttachRejected.
func TestLoadTaskDefinitions_TerminalUnknownFieldRejected(t *testing.T) {
	baseDir := t.TempDir()
	tasksDir := filepath.Join(baseDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tasksDir, "tmux.toml"), `
scope = "run"
setup = "echo '{}'"

[terminal]
attach     = "a"
capture    = "c"
sendtext   = "t"
send_keys  = "k"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadTaskDefinitions("")
	if err == nil {
		t.Fatal("expected an error for an unknown [terminal] field")
	}
	if !strings.Contains(err.Error(), "sendtext") {
		t.Errorf("error %q should name the unknown field", err.Error())
	}
}

// TestLoadTaskDefinitions_TerminalPartialTableRejected guards the ADR's
// load-time acceptance criterion: a [terminal] table declaring fewer than
// all four verbs must fail to load, naming the missing member(s).
func TestLoadTaskDefinitions_TerminalPartialTableRejected(t *testing.T) {
	baseDir := t.TempDir()
	tasksDir := filepath.Join(baseDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tasksDir, "tmux.toml"), `
scope = "run"
setup = "echo '{}'"

[terminal]
attach = "tmux attach -t {{.Self.session_name}}"
`)
	cfg := &Config{BaseDir: baseDir}
	_, err := cfg.LoadTaskDefinitions("")
	if err == nil {
		t.Fatal("expected an error for a partial [terminal] table")
	}
	for _, member := range []string{"capture", "send_text", "send_keys"} {
		if !strings.Contains(err.Error(), member) {
			t.Errorf("error %q does not name missing member %q", err.Error(), member)
		}
	}
}
