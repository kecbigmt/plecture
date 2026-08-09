// Package hook defines the contract types for tws hook stdin JSON.
//
// tws passes these structs as JSON via stdin to hook commands.
// Hook consumers import this package to parse hook input.
package hook

import (
	"encoding/json"
	"fmt"

	"github.com/kecbigmt/plect/contracts/state"
)

// Input is the JSON payload that tws sends to hook commands via stdin.
type Input struct {
	SessionName      string   `json:"session_name"`
	WorktreePath     string   `json:"worktree_path"`
	URL              string   `json:"url"`
	OwnerRepo        string   `json:"owner_repo"`
	Branch           string   `json:"branch"`
	TraceID          string   `json:"trace_id,omitempty"`
	HookArgs         []string `json:"hook_args"`
	ConversationJSON string   `json:"conversation_json"` // JSON-encoded Conversation, empty if none
	ChangeSummary    string   `json:"change_summary"`    // Human-readable change description (post_sync_change only)
	ChangeType       string   `json:"change_type"`       // Change type identifier (post_sync_change only)
}

// Conversation is an alias for state.Conversation, the shared type
// used across tws components for external communication channels.
type Conversation = state.Conversation

// ParseConversation parses the conversation_json field.
func (in *Input) ParseConversation() (*Conversation, error) {
	if in.ConversationJSON == "" {
		return nil, nil
	}
	var c Conversation
	if err := json.Unmarshal([]byte(in.ConversationJSON), &c); err != nil {
		return nil, fmt.Errorf("parsing conversation_json: %w", err)
	}
	return &c, nil
}

// ParseHookArgs parses named arguments from HookArgs (e.g. --template, --instruction).
func (in *Input) ParseHookArgs() (template, instruction string) {
	args := in.HookArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--template":
			if i+1 < len(args) {
				template = args[i+1]
				i++
			}
		case "--instruction":
			if i+1 < len(args) {
				instruction = args[i+1]
				i++
			}
		}
	}
	return
}
