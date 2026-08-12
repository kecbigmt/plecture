package dispatch

import (
	"testing"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/domain"
	contract "github.com/plecture/plect/contracts/state"
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
		Inputs: map[string]string{"path": "{{.Nodes.claude.outputs.socket_path}}"},
	}
	got, err := channelInputs(s, ch)
	if err != nil {
		t.Fatalf("channelInputs: %v", err)
	}
	if got["path"] != "/run/claude.sock" {
		t.Errorf("path = %v, want /run/claude.sock", got["path"])
	}
}

func TestChannelInputs_MissingNodeErrors(t *testing.T) {
	s := &domain.Session{Name: "o/r-1", Tasks: map[string]*contract.TaskState{}}
	ch := config.EventChannel{Name: "runtime", Inputs: map[string]string{"path": "{{.Nodes.claude.outputs.socket_path}}"}}
	if _, err := channelInputs(s, ch); err == nil {
		t.Fatal("expected error for a reference to a missing node")
	}
}
