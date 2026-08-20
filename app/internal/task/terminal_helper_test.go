package task

import (
	"context"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
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

// TestRunSetup_TerminalHelperResolvesSamePassOutputs covers the ordering the
// helper's contract depends on: the binding a caller hands RunSetup is built
// before the pass starts, so a downstream node's {{terminal "..."}} must see
// the outputs the declaring node produced during this same pass — not what
// its state held (nothing, on a fresh create or a --force-recreate) when the
// binding was taken.
func TestRunSetup_TerminalHelperResolvesSamePassOutputs(t *testing.T) {
	tests := []struct {
		name     string
		snapshot map[string]any
	}{
		{"fresh create or force-recreate leaves no prior outputs", map[string]any{}},
		{"a re-run supersedes the previous pass's outputs", map[string]any{"session_name": "stale-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := withScriptedExecutor(t, &scriptedExecutor{stdout: map[string]string{
				"start-terminal": `{"session_name":"fresh-1"}`,
			}})
			plan := buildPlan(t,
				[]taskStub{
					{
						id:       "terminal",
						setup:    "start-terminal",
						attach:   "attach -t {{.Self.session_name}}",
						capture:  "capture -t {{.Self.session_name}}",
						sendText: `send-text -t {{.Self.session_name}} -- "$1"`,
						sendKeys: `send-keys -t {{.Self.session_name}} "$1"`,
					},
					{id: "prompt", setup: `{{terminal "send_text"}}`},
				},
				[]nodeStub{
					{id: "terminal"},
					{id: "prompt", inputs: map[string]string{"session": "{{.Nodes.terminal.outputs.session_name}}"}},
				},
			)
			vars := SessionVars{Name: "s", Terminal: &TerminalBinding{
				Ops:     plan.TerminalTask().Terminal,
				Outputs: tt.snapshot,
			}}
			tasks := map[string]*contract.TaskState{}
			if err := RunSetup(context.Background(), plan.Run, vars, tasks, nil); err != nil {
				t.Fatalf("RunSetup: %v", err)
			}
			want := `send-text -t fresh-1 -- "$1"`
			if got := exec.commands(); len(got) != 2 || got[1] != want {
				t.Fatalf("commands = %q, want the second to be %q", got, want)
			}
		})
	}
}

// TestRunSetup_TerminalHelperKeepsSnapshotWhenDeclaringNodeIsElsewhere is the
// other half: a pass that does not run the declaring node has no fresher
// answer than the caller's binding, so it must render against that.
func TestRunSetup_TerminalHelperKeepsSnapshotWhenDeclaringNodeIsElsewhere(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	plan := buildPlan(t,
		[]taskStub{
			{
				id:       "terminal",
				scope:    "session",
				setup:    "start-terminal",
				attach:   "attach -t {{.Self.session_name}}",
				capture:  "capture -t {{.Self.session_name}}",
				sendText: `send-text -t {{.Self.session_name}} -- "$1"`,
				sendKeys: `send-keys -t {{.Self.session_name}} "$1"`,
			},
			{id: "prompt", scope: "run", setup: `{{terminal "send_text"}}`},
		},
		[]nodeStub{
			{id: "terminal"},
			{id: "prompt", inputs: map[string]string{"session": "{{.Nodes.terminal.outputs.session_name}}"}},
		},
	)
	vars := SessionVars{Name: "s", Terminal: &TerminalBinding{
		Ops:     plan.TerminalTask().Terminal,
		Outputs: map[string]any{"session_name": "produced-earlier"},
	}}
	tasks := map[string]*contract.TaskState{"terminal": {
		Scope:   contract.TaskScopeSession,
		Status:  contract.TaskStatusProduced,
		Outputs: map[string]any{"session_name": "produced-earlier"},
	}}
	if err := RunSetup(context.Background(), plan.Run, vars, tasks, nil); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	want := `send-text -t produced-earlier -- "$1"`
	if got := exec.commands(); len(got) != 1 || got[0] != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}
