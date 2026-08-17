package task

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// TestRender_TerminalHelperResolvesThroughSessionBinding exercises
// {{terminal "..."}} end-to-end through render, the same path a setup/
// cleanup hook string or a channel argument goes through — not just the
// unit-level RenderTerminalOp.
func TestRender_TerminalHelperResolvesThroughSessionBinding(t *testing.T) {
	binding := &TerminalBinding{
		Ops: &config.TerminalConfig{
			Attach:   "tmux attach -t {{.Self.session_name}}",
			Capture:  "tmux capture-pane -p -t {{.Self.session_name}}",
			SendText: `tmux send-keys -t {{.Self.session_name}} -- "$1"`,
			SendKeys: `tmux send-keys -t {{.Self.session_name}} "$1"`,
		},
		Outputs: map[string]any{"session_name": "owner/repo-1"},
	}

	tests := []struct {
		verb string
		want string
	}{
		{"attach", "tmux attach -t owner/repo-1"},
		{"capture", "tmux capture-pane -p -t owner/repo-1"},
		{"send_text", `tmux send-keys -t owner/repo-1 -- "$1"`},
		{"send_keys", `tmux send-keys -t owner/repo-1 "$1"`},
	}
	for _, tt := range tests {
		t.Run(tt.verb, func(t *testing.T) {
			out, err := render(`{{terminal "`+tt.verb+`"}}`, RenderContext{
				Session: SessionVars{Terminal: binding},
			})
			if err != nil {
				t.Fatalf("render: unexpected error: %v", err)
			}
			if out != tt.want {
				t.Errorf("render = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestRender_TerminalHelperNoBindingFailsLoud(t *testing.T) {
	if _, err := render(`{{terminal "send_text"}}`, RenderContext{Session: SessionVars{}}); err == nil {
		t.Fatal("render: want error when no task in the plan declares [terminal], got nil")
	}
}

func TestRenderTerminalOp_UnknownVerbFailsLoud(t *testing.T) {
	binding := &TerminalBinding{Ops: &config.TerminalConfig{Attach: "a", Capture: "c", SendText: "t", SendKeys: "k"}}
	_, err := RenderTerminalOp(binding, "quit", SessionVars{})
	if err == nil {
		t.Fatal("expected error for unknown terminal operation")
	}
	if !strings.Contains(err.Error(), "quit") {
		t.Fatalf("error %q should name the unknown verb", err.Error())
	}
}

// TestRender_TerminalHelperUsableFromChannelStyleTemplate mirrors the ADR's
// Codex terminal-submit example: {{terminal "..."}} embeds a fully-rendered
// command string as one literal channel argument, leaving the terminal
// task's own literal "$1" untouched (it is not a Go template action).
func TestRender_TerminalHelperUsableFromChannelStyleTemplate(t *testing.T) {
	binding := &TerminalBinding{
		Ops: &config.TerminalConfig{
			Attach:   "tmux attach -t mysession",
			Capture:  "tmux capture-pane -p -t mysession",
			SendText: `tmux send-keys -t mysession -- "$1"`,
			SendKeys: `tmux send-keys -t mysession "$1"`,
		},
	}
	out, err := render(`{{terminal "send_text"}}`, RenderContext{Session: SessionVars{Terminal: binding}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `tmux send-keys -t mysession -- "$1"`
	if out != want {
		t.Fatalf("render = %q, want %q (literal $1 must survive nested rendering)", out, want)
	}
}
