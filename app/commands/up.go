package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var (
	upTag        string
	upWorkflow   string
	upInputs     string
	upInputsFile string
	upTask       string
	upStream     string
	upParent     string
	upDetach     bool
)

var upCmd = &cobra.Command{
	Use:   "up <resource-id|session>",
	Short: "Run run-scoped tasks for a session",
	Long: `Run setup commands for all run-scoped tasks declared in config.toml,
in dependency-respecting order. Outputs from session-scoped tasks (already
set up by 'tws create') are available to run-scoped tasks via {{.Tasks.<id>.<key>}}.

After setup, docker-compose-style: on a TTY 'up' hands off to the workflow's
attach target by replacing the tws process via syscall.Exec.
Pass -d/--detach to skip the hand-off and return after setup, the way 'up'
behaved before. Non-TTY stdout (MCP, cron, scripts) is treated as --detach
automatically; the MCP tws_up tool is always detached. Workflows without an
attach target return after setup regardless of TTY.

--tag selects the session's workspace-identity label. Omitted, it defaults to
the workflow id (the same default 'tws create' applies), so 'tws up <resource-id>
--workflow X' converges on the session 'tws create' would make. Pass --tag to
resolve a specifically-labelled session (e.g. 'tws up <resource-id> --tag X'
matches 'tws create <resource-id> --tag X'). --tag is only valid with a
resource identifier, not a bare session name (the name already encodes the tag).

--inputs / --inputs-file forward session inputs to the auto-create path when
the session does not yet exist; passing them against an already-created
session returns an error (session inputs are set once, at create time).

--task <id> is shorthand for --inputs '{"task":"<id>"}' on the auto-create
path; see 'tws create --help' for the required/none semantics.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := resolveInputsFlags(upInputs, upInputsFile)
		if err != nil {
			return err
		}
		inputs, err = service.MergeTaskInput(inputs, upTask)
		if err != nil {
			return err
		}
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.Up(cfg, store, service.UpParams{
			Identifier:    args[0],
			Tag:           upTag,
			Workflow:      upWorkflow,
			Inputs:        inputs,
			StreamID:      upStream,
			ParentSession: upParent,
			Observer:      newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Session: %s\n", result.SessionName)

		if upDetach || !stdoutIsTerminal() {
			return nil
		}
		return autoAttach(cfg, store, result.SessionName)
	},
}

// autoAttach resolves the workflow's attach target and hands the TTY over via
// syscall.Exec, mirroring `tws attach`. A workflow without an attach target
// returns nil so `up` exits cleanly — the no-attach case is not an error,
// it's just the detached path.
func autoAttach(cfg *config.Config, store *state.Store, sessionName string) error {
	att, err := service.Attach(cfg, store, service.AttachParams{Identifier: sessionName})
	if err != nil {
		var svcErr *service.Error
		if errors.As(err, &svcErr) && svcErr.Code == service.ErrNotAttachable {
			return nil
		}
		return err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash not found in PATH: %w", err)
	}
	return syscall.Exec(bash, []string{"bash", "-c", att.Command}, os.Environ())
}

// stdoutIsTerminal reports whether os.Stdout is a TTY. Used by `up` to
// suppress auto-attach in non-interactive environments (MCP, cron, pipes).
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func init() {
	upCmd.Flags().StringVar(&upTag, "tag", "", "Workspace-identity label of the session to resolve/auto-create (resource-identifier only). Defaults to the workflow id, matching `tws create`.")
	upCmd.Flags().StringVarP(&upWorkflow, "workflow", "w", "", "Workflow id (auto-create path only; must match session's frozen workflow)")
	upCmd.Flags().StringVar(&upInputs, "inputs", "", "Session inputs as a JSON object string (auto-create path only)")
	upCmd.Flags().StringVar(&upInputsFile, "inputs-file", "", "Path to a JSON file containing the session inputs object (auto-create path only)")
	upCmd.Flags().StringVar(&upTask, "task", "", "Shorthand for --inputs '{\"task\":\"<id>\"}' (auto-create path only). Pass \"none\" for no initial task.")
	upCmd.Flags().StringVar(&upStream, "stream", "", "Opaque work-stream id for the session and its events (auto-create path only; an existing session keeps its create-time stream). Falls back to $TWS_STREAM_ID")
	upCmd.Flags().StringVar(&upParent, "parent", "", "Parent session name for auto-created sessions, or \"root:<session>\" to join that (possibly parentless) session's siblings; falls back to $TWS_SESSION_NAME when it names an existing session")
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Return after setup instead of attaching (docker-compose-style up -d)")
	rootCmd.AddCommand(upCmd)
}
