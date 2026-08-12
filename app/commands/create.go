package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/service"
	"github.com/cradel-dev/cradel/app/internal/state"
)

var (
	createTag        string
	createWorkflow   string
	createInputs     string
	createInputsFile string
	createTask       string
	createParent     string
)

var createCmd = &cobra.Command{
	Use:   "create <resource-id>",
	Short: "Create a session for a resource and run session-scoped tasks",
	Long: `Create a session: derive the session id from the resource identifier,
acquire the working directory (workflow setup), and run session-scoped
tasks. Does not auto-start the runtime — use 'sennit up' next to launch
run-scoped tasks.

The resource identifier is any string. A workflow whose [resolver] matches
is auto-selected;
otherwise pass --workflow — with a resolver-less workflow the identifier
itself becomes the session id.

--tag is the session's workspace-identity label. When omitted it defaults to
the workflow id, so two tools acting on one resource (e.g. claude work and
codex review) get distinct, non-colliding workspaces without the caller having
to spell out a tag. Pass --tag explicitly to label a retry/experiment/parallel
session. The label is provider-agnostic; a provider may materialize it as
a branch/worktree suffix, but that mapping is an implementation detail.

Pass session inputs via --inputs '{"key":"value"}' or --inputs-file <path>.
Inputs are validated against [inputs_schema] (workflow file when one is
selected; falls back to the top-level config.toml for the legacy inline-
tasks path), frozen at create time, and exposed to setup/cleanup
templates as {{.Inputs.<key>}}.

--task <id> is shorthand for --inputs '{"task":"<id>"}': it sets the
session's initial task without hand-writing JSON. Workflows that declare
"task" as required (e.g. claude, codex) reject a create without it; pass
--task none for an ad-hoc session with no initial instruction.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := resolveInputsFlags(createInputs, createInputsFile)
		if err != nil {
			return err
		}
		inputs, err = service.MergeTaskInput(inputs, createTask)
		if err != nil {
			return err
		}
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.Create(cfg, store, service.CreateParams{
			URL:           args[0],
			Tag:           createTag,
			Workflow:      createWorkflow,
			Inputs:        inputs,
			ParentSession: createParent,
			Observer:      newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		if result.ReusedWorktree {
			fmt.Fprintf(os.Stderr, "Worktree already exists: %s\n", result.WorktreePath)
		} else {
			fmt.Fprintf(os.Stderr, "Created worktree: %s\n", result.WorktreePath)
		}
		fmt.Fprintf(os.Stderr, "Session: %s (branch: %s)\n", result.SessionName, result.Branch)
		return nil
	},
}

func init() {
	createCmd.Flags().StringVar(&createTag, "tag", "", "Workspace-identity label for the session (e.g. review). Defaults to the workflow id, so two tools on one resource get separate workspaces. Provider-agnostic: a provider may map it to a branch/worktree suffix, but that is an implementation detail.")
	createCmd.Flags().StringVarP(&createWorkflow, "workflow", "w", "", "Workflow id — filename stem of .sennit/workflows/<id>.toml")
	createCmd.Flags().StringVar(&createInputs, "inputs", "", "Session inputs as a JSON object string")
	createCmd.Flags().StringVar(&createInputsFile, "inputs-file", "", "Path to a JSON file containing the session inputs object")
	createCmd.Flags().StringVar(&createTask, "task", "", "Shorthand for --inputs '{\"task\":\"<id>\"}'. Pass \"none\" for no initial task.")
	createCmd.Flags().StringVar(&createParent, "parent", "", "Parent session name for the session tree, or \"root:<session>\" to join that (possibly parentless) session's siblings; falls back to $SENNIT_SESSION_NAME when it names an existing session")
	rootCmd.AddCommand(createCmd)
}
