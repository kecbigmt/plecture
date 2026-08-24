package task

import (
	"context"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// paneVerbs is a terminal endpoint declared the way a multiplexer declares
// one: each verb an exec action naming the pane it acts on, so a resolved
// verb is a command a consuming script can run and assert on.
func paneVerbs() *config.TerminalConfig {
	verb := func(args ...any) *lang.Action {
		action := &lang.Action{Type: lang.ActionExec, Command: "tmux"}
		for _, arg := range args {
			switch v := arg.(type) {
			case string:
				action.Args = append(action.Args, &lang.Value{Form: lang.FormLiteral, Literal: v})
			case *lang.Value:
				action.Args = append(action.Args, v)
			}
		}
		return action
	}
	pane := &lang.Value{Form: lang.FormFrom, From: "self.outputs.session_name"}
	return &config.TerminalConfig{
		Attach:   verb("attach", "-t", pane),
		Capture:  verb("capture-pane", "-p", "-t", pane),
		SendText: verb("send-keys", "-t", pane, "--"),
		SendKeys: verb("send-keys", "-t", pane),
		PID:      verb("display-message", "-p", "-t", pane, "#{pane_pid}"),
	}
}

// A consumed verb resolves against the declaring effect's own outputs, and
// arrives as a command whose trailing "$@" is where the consumer's operand
// lands.
func TestTerminalCommand_ResolvesEveryVerb(t *testing.T) {
	binding := &TerminalBinding{Ops: paneVerbs(), Outputs: map[string]any{"session_name": "owner/repo-1"}}
	tests := []struct {
		verb string
		want string
	}{
		{"attach", `'tmux' 'attach' '-t' 'owner/repo-1' "$@"`},
		{"capture", `'tmux' 'capture-pane' '-p' '-t' 'owner/repo-1' "$@"`},
		{"send_text", `'tmux' 'send-keys' '-t' 'owner/repo-1' '--' "$@"`},
		{"send_keys", `'tmux' 'send-keys' '-t' 'owner/repo-1' "$@"`},
		{"pid", `'tmux' 'display-message' '-p' '-t' 'owner/repo-1' '#{pane_pid}' "$@"`},
	}
	for _, tt := range tests {
		t.Run(tt.verb, func(t *testing.T) {
			out, err := TerminalCommand(binding, tt.verb, SessionVars{Name: "owner/repo-1"}, t.TempDir())
			if err != nil {
				t.Fatalf("TerminalCommand: %v", err)
			}
			if out != tt.want {
				t.Errorf("TerminalCommand = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestTerminalCommand_NoEndpointFailsLoud(t *testing.T) {
	if _, err := TerminalCommand(nil, "send_text", SessionVars{}, t.TempDir()); err == nil {
		t.Fatal("want an error when no effect in the plan declares an interactive endpoint")
	}
}

func TestTerminalCommand_UnknownVerbFailsLoud(t *testing.T) {
	binding := &TerminalBinding{Ops: paneVerbs()}
	_, err := TerminalCommand(binding, "quit", SessionVars{}, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an unknown terminal verb")
	}
	if !strings.Contains(err.Error(), "quit") {
		t.Fatalf("error %q should name the unknown verb", err.Error())
	}
}

// A shell verb is materialized rather than inlined, so its own bindings stay
// out of the command a consumer runs: what the consumer receives is a path,
// and the operand still reaches the verb's positional parameters.
func TestTerminalCommand_ShellVerbResolvesToItsWrapper(t *testing.T) {
	binding := &TerminalBinding{
		Ops: &config.TerminalConfig{SendText: &lang.Action{
			Type:   lang.ActionShell,
			Script: `tmux send-keys -t "$pane" -- "$1"`,
			Bind:   map[string]*lang.Value{"pane": {Form: lang.FormFrom, From: "self.outputs.session_name"}},
		}},
		Outputs: map[string]any{"session_name": "owner/repo-1"},
	}
	out, err := TerminalCommand(binding, "send_text", SessionVars{}, t.TempDir())
	if err != nil {
		t.Fatalf("TerminalCommand: %v", err)
	}
	if strings.Contains(out, "owner/repo-1") {
		t.Errorf("TerminalCommand = %q, want the bound pane to stay out of the command", out)
	}
	if !strings.HasSuffix(out, `"$@"`) {
		t.Errorf("TerminalCommand = %q, want the consumer's operand to reach the verb", out)
	}
}

// TestRunSetup_TerminalCapabilityResolvesSamePassOutputs covers the ordering
// the capability's contract depends on: the binding a caller hands RunSetup
// is built before the pass starts, so a downstream node consuming a verb must
// see the outputs the declaring node produced during this same pass — not
// what its state held (nothing, on a fresh create or a --force-recreate) when
// the binding was taken.
func TestRunSetup_TerminalCapabilityResolvesSamePassOutputs(t *testing.T) {
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
					{id: "terminal", setup: "start-terminal", attach: "attach", capture: "capture", sendText: "send-text", sendKeys: "send-keys"},
					{id: "prompt", setupAction: consumesVerb("send_text")},
				},
				[]nodeStub{
					{id: "terminal"},
					{id: "prompt", inputs: map[string]*lang.Value{"session": fromValue("nodes.terminal.outputs.session_name")}},
				},
			)
			plan.Run[0].Terminal = paneVerbs()
			vars := SessionVars{Name: "s", Terminal: &TerminalBinding{Ops: paneVerbs(), Outputs: tt.snapshot}}
			tasks := map[string]*contract.TaskState{}
			if err := RunSetup(context.Background(), plan.Run, vars, tasks, nil); err != nil {
				t.Fatalf("RunSetup: %v", err)
			}
			if len(exec.bindings) != 2 {
				t.Fatalf("executions = %d, want 2", len(exec.bindings))
			}
			if !strings.Contains(exec.bindings[1], "fresh-1") {
				t.Fatalf("bindings = %q, want the verb resolved against this pass's outputs", exec.bindings[1])
			}
		})
	}
}

// TestRunSetup_TerminalCapabilityKeepsSnapshotWhenDeclaringNodeIsElsewhere is
// the other half: a pass that does not run the declaring node has no fresher
// answer than the caller's binding, so it must resolve against that.
func TestRunSetup_TerminalCapabilityKeepsSnapshotWhenDeclaringNodeIsElsewhere(t *testing.T) {
	exec := withScriptedExecutor(t, &scriptedExecutor{})
	plan := buildPlan(t,
		[]taskStub{
			{id: "terminal", scope: "session", setup: "start-terminal", attach: "attach", capture: "capture", sendText: "send-text", sendKeys: "send-keys"},
			{id: "prompt", scope: "run", setupAction: consumesVerb("send_text")},
		},
		[]nodeStub{
			{id: "terminal"},
			{id: "prompt", inputs: map[string]*lang.Value{"session": fromValue("nodes.terminal.outputs.session_name")}},
		},
	)
	vars := SessionVars{Name: "s", Terminal: &TerminalBinding{
		Ops:     paneVerbs(),
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
	if len(exec.bindings) != 1 {
		t.Fatalf("executions = %d, want 1", len(exec.bindings))
	}
	if !strings.Contains(exec.bindings[0], "produced-earlier") {
		t.Fatalf("bindings = %q, want the verb resolved against the caller's snapshot", exec.bindings[0])
	}
}

// consumesVerb is a setup that runs one terminal verb the way a shipped
// launch sequence does: the verb arrives as a bound value and the script runs
// it, so the operand is the script's own and the verb never becomes part of
// this script's source.
func consumesVerb(verb string) *lang.Action {
	return &lang.Action{
		Type:   lang.ActionShell,
		Script: `sh -c "$run" terminal-verb hello`,
		Bind:   map[string]*lang.Value{"run": {Form: lang.FormTerminal, Terminal: verb}},
	}
}
