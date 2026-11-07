package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var (
	upTag        string
	upWorkflow   string
	upInputs     string
	upInputsFile string
	upTask       string
	upTaskRes    string
	upParent     string
	upDetach     bool
	upRecreate   bool
	upJSON       bool
)

var upCmd = &cobra.Command{
	Use:   "up <resource-id|session>",
	Short: "Run run-scoped tasks for a session",
	Long: `Run setup commands for all run-scoped tasks declared in config.toml,
in dependency-respecting order. Outputs from session-scoped tasks (already
set up during auto-create) are available to run-scoped tasks via {{.Tasks.<id>.<key>}}.

After setup, docker-compose-style: on a TTY 'up' hands off to the workflow's
attach target by replacing the plect process via syscall.Exec.
Pass -d/--detach to skip the hand-off and return after setup, the way 'up'
behaved before. Non-TTY stdout (MCP, cron, scripts) is treated as --detach
automatically; the MCP plect_up tool is always detached. Workflows without an
attach target return after setup regardless of TTY.

A resource identifier selects the one workflow whose entry resource is the
identifier's resource type; pass --workflow when several accept it. The
session is named after the resource name, so repeated
'plect up <resource-id>' calls converge on the same session.

--tag appends one segment to that name ('<resource-name>+<tag>', the tag matching
[A-Za-z0-9_-]+) to run a second session for the same resource. --tag is only
valid with a resource identifier, not a bare session name (the name already
encodes the tag). An existing session with the composed name but a different
entry resource or tag is a collision, not the same session.

--inputs / --inputs-file forward session inputs to the auto-create path when
the session does not yet exist; passing them against an already-created
session returns an error (session inputs are set once, at create time).

--task <id> selects the task document instantiated as the session's
'initial' instance; omit it for a session a person works in with no task.
The task binds the entry resource unless --task-resource names another
concrete resource. Either way the task's resource type must claim that
resource, or up fails before creating anything. Repeating the same
selection against an existing session is a no-op; selecting a different one
fails until 'plect task cleanup initial' removes the existing instance.

--force-recreate rebuilds the session runtime for an existing session while
keeping its durable identity and event log. It cleans and forgets workflow
node state, dynamic task instances, environment state, and runtime observation
state so the next setup cannot resume from .Prev, then runs setup again.

If max_up_children is full, up accepts the session as sleeping and prints its
queue position. --json reports result and queue_position for scripts.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := resolveInputsFlags(upInputs, upInputsFile)
		if err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		result, err := service.Up(cfg, store, service.UpParams{
			Identifier:     args[0],
			Tag:            upTag,
			Workflow:       upWorkflow,
			Inputs:         inputs,
			InitialTask:    service.InitialTaskSelection(upTask, upTaskRes),
			ParentSession:  upParent,
			Observer:       newTaskObserver(cfg),
			ForceRecreate:  upRecreate,
			HumanInitiated: true,
			InvocationDir:  invocationDir(),
		})
		if err != nil {
			return err
		}
		if upJSON {
			b, err := json.Marshal(result)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		if result.Result == "sleeping" {
			fmt.Fprintf(cmd.OutOrStdout(), "accepted: sleeping (queue position %d)\n", result.QueuePosition)
			return nil
		}
		fmt.Fprintf(os.Stderr, "Session: %s\n", result.SessionName)

		if upDetach || !stdoutIsTerminal() {
			return nil
		}
		return autoAttach(cfg, store, result.SessionName)
	},
}

// autoAttach resolves the workflow's attach target and hands the TTY over via
// syscall.Exec, mirroring `plect attach`. A workflow without an attach target
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
	upCmd.Flags().StringVar(&upTag, "tag", "", "Segment appended to the resource name to distinguish another session for the same resource (resource-identifier only).")
	upCmd.Flags().StringVarP(&upWorkflow, "workflow", "w", "", "Workflow id (auto-create path only; must match session's frozen workflow)")
	upCmd.Flags().StringVar(&upInputs, "inputs", "", "Session inputs as a JSON object string (auto-create path only)")
	upCmd.Flags().StringVar(&upInputsFile, "inputs-file", "", "Path to a JSON file containing the session inputs object (auto-create path only)")
	upCmd.Flags().StringVar(&upTask, "task", "", "Task document to instantiate as the session's initial instance (resource-identifier only)")
	upCmd.Flags().StringVar(&upTaskRes, "task-resource", "", "Concrete resource the initial task binds (default: the entry resource identifier; requires --task)")
	upCmd.Flags().StringVar(&upParent, "parent", "", "Parent session name for auto-created sessions, or \"root:<session>\" to join that (possibly parentless) session's siblings; falls back to $PLECT_SESSION_NAME when it names an existing session")
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Return after setup instead of attaching (docker-compose-style up -d)")
	upCmd.Flags().BoolVar(&upRecreate, "force-recreate", false, "Rebuild the session runtime instead of resuming existing task outputs")
	upCmd.Flags().BoolVar(&upJSON, "json", false, "Output the accepted result as JSON")
	rootCmd.AddCommand(upCmd)
}
