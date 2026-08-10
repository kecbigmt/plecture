package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/app/internal/state"
)

var (
	setConvSource     string
	setConvURL        string
	setConvMeta       []string
	setOutputNode     string
	setOutputWorkflow bool
	setOutputTask     string
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Manage session state",
}

var setConversationCmd = &cobra.Command{
	Use:   "set-conversation <session-or-url>",
	Short: "Set the conversation associated with a session",
	Long: `Set or update the conversation (e.g., Slack thread) linked to a session.

Example:
  sennit state set-conversation owner/repo-1 \
    --source Slack \
    --url "https://exampleorg.slack.com/archives/C.../p..." \
    --meta thread_ts=1234567890.123456 \
    --meta channel_id=C01ABCDEF`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")

		metadata := make(map[string]string)
		for _, m := range setConvMeta {
			parts := strings.SplitN(m, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --meta format %q: expected key=value", m)
			}
			metadata[parts[0]] = parts[1]
		}

		conv := &domain.Conversation{
			Source:   setConvSource,
			URL:      setConvURL,
			Metadata: metadata,
		}

		if err := service.SetConversation(cfg, store, args[0], conv); err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Conversation set for %s\n", args[0])
		return nil
	},
}

var setMessageCmd = &cobra.Command{
	Use:   "set-message <session-or-url> <text>",
	Short: "Set the session's self-reported status message",
	Long: `Set or clear the session-level status message. sennit does not interpret
the text — it is a slot for external self-reports (e.g. an agent's
turn-boundary hook announcing "working" / "waiting").

An empty string clears the message.

Example:
  sennit state set-message owner/repo-1 "working"
  sennit state set-message owner/repo-1 ""`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")

		if err := service.SetMessage(cfg, store, args[0], args[1]); err != nil {
			return err
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Message set for %s\n", args[0])
		return nil
	},
}

var setOutputCmd = &cobra.Command{
	Use:   "set-output <session-or-url> <json>",
	Short: "Merge values into a produced task's outputs",
	Long: `Merge a JSON object into the persisted outputs of a produced task.

Targets a workflow node (--node <id>), the workflow pseudo-node (--workflow),
or a runtime task handle (--task <task-handle>, e.g. review#1).
Merge-only: keys absent from the payload are left untouched. Only keys declared
with ` + "`mutable = true`" + ` in the target's outputs schema are writable; the reserved
"workdir" key is always immutable.

Intended for external updaters (e.g. a watcher daemon refreshing observed
values like pr_state) — resources created by setup stay immutable.

Note: validation is against the target's CURRENT outputs schema. If a def drifts
after instantiation (a key made immutable or dropped from properties, or a new
required output added), the merge is rejected and the observed value silently
stops updating — check the watcher's logs if a done_when stalls.

Example:
  sennit state set-output owner/repo-1 --node github_watch '{"pr_state":"merged"}'
  sennit state set-output owner/repo-1 --workflow '{"title":"New title"}'
  sennit state set-output owner/repo-1 --task review#1 '{"checks_status":"SUCCESS"}'`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var payload map[string]any
		if err := json.Unmarshal([]byte(args[1]), &payload); err != nil {
			return fmt.Errorf("payload is not a JSON object: %w", err)
		}
		if payload == nil {
			return fmt.Errorf("payload is not a JSON object: got null")
		}
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.SetOutput(cfg, store, service.SetOutputParams{
			Identifier: args[0],
			Node:       setOutputNode,
			Workflow:   setOutputWorkflow,
			Task:       setOutputTask,
			Outputs:    payload,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Updated outputs of %s for %s: %s\n", result.Target, result.SessionName, strings.Join(result.Keys, ", "))
		return nil
	},
}

func init() {
	setConversationCmd.Flags().StringVar(&setConvSource, "source", "", "Conversation source (e.g., Slack, Discord)")
	setConversationCmd.Flags().StringVar(&setConvURL, "url", "", "Permalink URL to the conversation")
	setConversationCmd.Flags().StringArrayVar(&setConvMeta, "meta", nil, "Metadata key=value pairs (repeatable)")
	setConversationCmd.MarkFlagRequired("source")
	setConversationCmd.MarkFlagRequired("url")

	setOutputCmd.Flags().StringVar(&setOutputNode, "node", "", "Target workflow node id")
	setOutputCmd.Flags().BoolVar(&setOutputWorkflow, "workflow", false, "Target the workflow pseudo-node")
	setOutputCmd.Flags().StringVar(&setOutputTask, "task", "", "Target a produced runtime task such as review#1")
	setOutputCmd.MarkFlagsMutuallyExclusive("node", "workflow", "task")
	setOutputCmd.MarkFlagsOneRequired("node", "workflow", "task")

	stateCmd.AddCommand(setConversationCmd)
	stateCmd.AddCommand(setMessageCmd)
	stateCmd.AddCommand(setOutputCmd)
	rootCmd.AddCommand(stateCmd)
}
