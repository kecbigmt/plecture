package dispatch

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestChannelInputs_ResolvesNodeOutputs(t *testing.T) {
	s := &domain.Session{
		Name: "o/r-1",
		Tasks: map[string]*contract.TaskState{
			"claude": {Outputs: map[string]any{"socket_path": "/run/claude.sock"}},
			"tmux":   {Outputs: map[string]any{"session_name": "o/r-1"}},
		},
	}
	ch := config.EventChannel{
		Name:   "runtime",
		Uses:   "claude_channel",
		Inputs: map[string]*lang.Value{"path": fromValue("nodes.claude.outputs.socket_path")},
	}
	got, err := channelInputs(s, ch, config.ChannelDefinition{})
	if err != nil {
		t.Fatalf("channelInputs: %v", err)
	}
	if got["path"] != "/run/claude.sock" {
		t.Errorf("path = %v, want /run/claude.sock", got["path"])
	}
}

func TestChannelInputs_MissingNodeErrors(t *testing.T) {
	s := &domain.Session{Name: "o/r-1", Tasks: map[string]*contract.TaskState{}}
	ch := config.EventChannel{Name: "runtime", Inputs: map[string]*lang.Value{"path": fromValue("nodes.claude.outputs.socket_path")}}
	if _, err := channelInputs(s, ch, config.ChannelDefinition{}); err == nil {
		t.Fatal("expected error for a reference to a missing node")
	}
}

func TestChannelInputs_FillsDeclaredDefaultsTheWorkflowLeftUnset(t *testing.T) {
	s := &domain.Session{Name: "o/r-1", Tasks: map[string]*contract.TaskState{
		"codex_exec": {Outputs: map[string]any{"queue_dir": "/q"}},
	}}
	ch := config.EventChannel{
		Name:   "runtime",
		Uses:   "codex_exec",
		Inputs: map[string]*lang.Value{"queue_dir": fromValue("nodes.codex_exec.outputs.queue_dir")},
	}
	def := config.ChannelDefinition{InputSchema: map[string]config.ChannelInputSpec{
		"queue_dir":       {Type: "string", Required: true},
		"enqueue_timeout": {Type: "string", Default: "5s", HasDefault: true},
	}}
	got, err := channelInputs(s, ch, def)
	if err != nil {
		t.Fatalf("channelInputs: %v", err)
	}
	if got["queue_dir"] != "/q" || got["enqueue_timeout"] != "5s" {
		t.Errorf("inputs = %v", got)
	}
}
