package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var (
	taskSetupSession      string
	taskSetupName         string
	taskSetupResource     string
	taskSetupInputs       []string
	taskSetupDoneWhenJSON string

	taskCleanupSession string

	taskFinalizeSession string
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Instantiate and reclaim tasks at runtime",
}

var taskSetupCmd = &cobra.Command{
	Use:   "setup <task-id>",
	Short: "Instantiate a task against the current session",
	Long: `Instantiate a task definition at runtime (ADR-003 dynamic instantiation).

The same task definition that a workflow can wire as a static DAG node is
instantiated here on demand: its setup runs, the instance (outputs + cleanup +
scope) is registered in session state and shown by 'plect status', and teardown
reclaims it in reverse-instantiation order.

Scope governs the lifecycle:
  - run-scoped tasks may only be instantiated while the run scope is up
    (after 'plect up'); they are cleaned at 'plect down'.
  - session-scoped tasks may be instantiated any time and are cleaned at
    'plect destroy'.

The session defaults to the ambient pane environment ($PLECT_SESSION_NAME,
exported into the agent's shell), so a running agent can simply
'plect task setup <id>'. --session overrides it.

Without --name, each setup creates a fresh numbered instance (<task>#<n>), so
the same task can be instantiated any number of times. --name pins a
session-global singleton: the instance key IS the name and a second
'setup --name <name>' is a collision error — recreate it by running
'plect task cleanup <name>' first. --resource binds the external resource this
instance works on (exposed to its setup/done_when as .ResourceID); it is
decoupled from the instance's identity. Inputs the task declares are bound
from --input first, then the workflow/provider outputs, then the session inputs.
--done-when-json appends additional done_when leaves to this instance only.

Examples:
  plect task setup work --input instruction="fix the flaky test"
  plect task setup review --name initial --resource <resource-id>
  plect task setup work --done-when-json '{"all":[{"judge":"Codex review approved","id":"codex-review","relation":["sibling"]}]}'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputs, err := parseKeyValues(taskSetupInputs)
		if err != nil {
			return err
		}
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.TaskSetup(cfg, store, service.TaskSetupParams{
			TaskID:            args[0],
			SessionName:       taskSetupSession,
			Name:              taskSetupName,
			Resource:          taskSetupResource,
			Inputs:            inputs,
			ExtraDoneWhenJSON: taskSetupDoneWhenJSON,
			Observer:          newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Instantiated %s [%s] for %s\n", taskDisplayName(result.Name, result.Instance), result.Scope, result.SessionName)
		return nil
	},
}

var taskCleanupCmd = &cobra.Command{
	Use:   "cleanup <instance>",
	Short: "Reclaim a single dynamic task instance",
	Long: `Tear down one dynamic task instance: run its cleanup script and remove it
from session state. The single-instance counterpart of 'plect down' / 'plect
destroy'.

The instance is addressed by its key alone — a --name (e.g. 'initial') or a
numbered '<task>#<n>' — and reclaimed regardless of which task produced it
(so a task drift that left 'initial' on the wrong task is still swept). A
missing instance is a no-op (exit 0), so 'cleanup <name>; setup … --name <name>'
is a safe recreate idiom.

The session defaults to $PLECT_SESSION_NAME; --session overrides it.

Examples:
  plect task cleanup initial
  plect task cleanup review#1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.TaskCleanup(cfg, store, service.TaskCleanupParams{
			Instance:    args[0],
			SessionName: taskCleanupSession,
			Observer:    newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		if result.Found {
			fmt.Fprintf(os.Stderr, "Reclaimed %s from %s\n", result.Instance, result.SessionName)
		} else {
			fmt.Fprintf(os.Stderr, "No instance %s in %s (nothing to clean)\n", result.Instance, result.SessionName)
		}
		return nil
	},
}

var taskFinalizeCmd = &cobra.Command{
	Use:   "finalize <instance>",
	Short: "Confirm done_when is satisfied and let the bound resource record completion",
	Long: `Finalize is the step ADR "goal-as-task" D4 places between done_when being
satisfied and teardown: it reconfirms the instance's done_when is satisfied at
the current revision (refusing outright if it is not — finalize never forces
completion), then lets the bound --resource's definition record completion via
its 'finalize' script if it declares one. A resource definition without a
'finalize' script (every kind until a later ADR slice adds one, e.g. a local
OKF goal) is a no-op step, not an error.

Finalize is "gate + record" only: it never tears the instance down. Run
'plect task cleanup <instance>' separately once you're done observing it.

The session defaults to $PLECT_SESSION_NAME; --session overrides it.

Example:
  plect task finalize goal_flaky-tests`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.FinalizeTask(cfg, store, service.FinalizeTaskParams{
			Instance:    args[0],
			SessionName: taskFinalizeSession,
		})
		if err != nil {
			return err
		}
		if result.Finalized {
			fmt.Fprintf(os.Stderr, "Finalized %s (resource %s via %s). Run 'plect task cleanup %s' to reclaim it.\n", result.Instance, result.ResourceID, result.Definition, result.Instance)
		} else {
			fmt.Fprintf(os.Stderr, "Finalized %s (no resource finalize step declared). Run 'plect task cleanup %s' to reclaim it.\n", result.Instance, result.Instance)
		}
		return nil
	},
}

// parseKeyValues turns repeated "key=value" flags into a map. An entry without
// '=' is an error so a typo surfaces instead of binding an empty value.
func parseKeyValues(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --input %q: expected key=value", p)
		}
		out[k] = v
	}
	return out, nil
}

func init() {
	taskSetupCmd.Flags().StringVar(&taskSetupSession, "session", "", "Target session (defaults to $PLECT_SESSION_NAME)")
	taskSetupCmd.Flags().StringVar(&taskSetupName, "name", "", "Instance identity: the key becomes the name (session-global singleton); collides on re-setup")
	taskSetupCmd.Flags().StringVar(&taskSetupResource, "resource", "", "External resource bound to the instance (exposed as .ResourceID; not part of the key)")
	taskSetupCmd.Flags().StringArrayVar(&taskSetupInputs, "input", nil, "Input binding key=value (repeatable)")
	taskSetupCmd.Flags().StringVar(&taskSetupDoneWhenJSON, "done-when-json", "", "Additional done_when JSON appended to this dynamic instance")
	taskCleanupCmd.Flags().StringVar(&taskCleanupSession, "session", "", "Target session (defaults to $PLECT_SESSION_NAME)")
	taskFinalizeCmd.Flags().StringVar(&taskFinalizeSession, "session", "", "Target session (defaults to $PLECT_SESSION_NAME)")
	taskCmd.AddCommand(taskSetupCmd)
	taskCmd.AddCommand(taskCleanupCmd)
	taskCmd.AddCommand(taskFinalizeCmd)
	rootCmd.AddCommand(taskCmd)
}
