package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var (
	taskSetupSession      string
	taskSetupName         string
	taskSetupResource     string
	taskSetupInputs       []string
	taskSetupDoneWhenJSON string

	taskCleanupSession string

	taskFinalizeSession string

	taskShowJSON bool
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
decoupled from the instance's identity. Binding implies delivery: when a
workspace provider recognizes --resource, it is also registered for event
delivery to the session (the same registration 'plect subscribe' performs),
so done_when re-evaluation and chain firing react to its events instead of
waiting for the heartbeat. 'plect task cleanup' drops that registration once
nothing else in the session still needs it. Inputs the task declares are bound
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
		cfg, err := config.Load()
		if err != nil {
			return err
		}
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
		if result.Subscribed {
			fmt.Fprintf(os.Stderr, "Registered %s for event delivery to %s\n", result.Resource, result.SessionName)
		} else if result.SubscribeError != "" {
			fmt.Fprintf(os.Stderr, "Warning: could not register %s for event delivery: %s\n", result.Resource, result.SubscribeError)
		}
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
		cfg, err := config.Load()
		if err != nil {
			return err
		}
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
			if result.Unsubscribed {
				fmt.Fprintf(os.Stderr, "Dropped event delivery for %s from %s\n", result.Resource, result.SessionName)
			} else if result.UnsubscribeError != "" {
				fmt.Fprintf(os.Stderr, "Warning: could not drop event delivery for %s: %s\n", result.Resource, result.UnsubscribeError)
			}
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
'finalize' script is a no-op step, not an error.

Finalize is "gate + record" only: it never tears the instance down. Run
'plect task cleanup <instance>' separately once you're done observing it.

The session defaults to $PLECT_SESSION_NAME; --session overrides it.

Example:
  plect task finalize goal_flaky-tests`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
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

var taskShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show a task definition, including its nesting chain",
	Long: `Print one task definition as the cascade resolves it: its id, the scope its
instances take, the file it was loaded from, and — when the task is nested —
every layer of the chain from the outermost task inward to the innermost one,
with each layer's 'inner' reference and the file it resolved to.

Use --json for the same picture as a structured document.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		detail, err := service.TaskShow(cfg, cwd, args[0])
		if err != nil {
			return err
		}
		if taskShowJSON {
			b, err := json.MarshalIndent(detail, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		return writeTaskDetail(cmd.OutOrStdout(), detail)
	},
}

// writeTaskDetail renders the header fields as an aligned block and the
// nesting chain as an indented outline, matching `workflow show`'s node
// listing: the indent is what carries "inner", so no arrows are needed.
func writeTaskDetail(out io.Writer, d *service.TaskDetail) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID:\t%s\n", d.ID)
	fmt.Fprintf(w, "Scope:\t%s\n", d.Scope)
	if d.SourcePath != "" {
		fmt.Fprintf(w, "Source:\t%s\n", d.SourcePath)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(d.Nesting) == 0 {
		return nil
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Nesting chain (outermost first):")
	for i, layer := range d.Nesting {
		line := strings.Repeat("  ", i+1) + layer.ID
		if layer.Inner != "" {
			line += fmt.Sprintf(" (inner = %q)", layer.Inner)
		}
		fmt.Fprintln(out, line)
	}
	return nil
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
	taskShowCmd.Flags().BoolVar(&taskShowJSON, "json", false, "Output the definition as JSON")
	taskCmd.AddCommand(taskShowCmd)
	rootCmd.AddCommand(taskCmd)
}
