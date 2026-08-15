// Command plect-okf is the executable the shipped okf plugin config invokes
// for its resource, provider, and task hooks. Each subcommand prints the
// contract its TOML caller expects on stdout (JSON for resource/provider
// hooks, nothing on task hooks beyond a created-instance summary) and
// reports failure through its exit status.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/plugins/okf/internal/cliexec"
	"github.com/kecbigmt/plecture/plugins/okf/internal/goal"
	"github.com/kecbigmt/plecture/plugins/okf/internal/resource"
	"github.com/kecbigmt/plecture/plugins/okf/internal/task"
	"github.com/kecbigmt/plecture/plugins/okf/internal/workspace"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "plect-okf:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: plect-okf <resource|provider|task> <subcommand> [flags]")
	}
	group, sub, rest := args[0], args[1], args[2:]

	switch group {
	case "resource":
		switch sub {
		case "observe":
			return runResourceObserve(rest)
		case "finalize":
			return runResourceFinalize(rest)
		}
	case "provider":
		switch sub {
		case "setup":
			return runProviderSetup(rest)
		case "cleanup":
			return runProviderCleanup(rest)
		}
	case "task":
		switch sub {
		case "validate-goal-resource":
			return runTaskValidateGoalResource(rest)
		case "bootstrap":
			return runTaskBootstrap(rest)
		}
	}
	return fmt.Errorf("unknown subcommand %q %q; expected one of: resource observe, resource finalize, provider setup, provider cleanup, task validate-goal-resource, task bootstrap", group, sub)
}

func runResourceObserve(args []string) error {
	fs := flag.NewFlagSet("resource observe", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier (local-okf://<owner>/goals/<slug>.md)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resourceID == "" {
		return fmt.Errorf("resource observe requires --resource")
	}

	result, err := resource.Observe(cliexec.CLI{}, *resourceID)
	if err != nil {
		return err
	}

	return encodeJSON(os.Stdout, map[string]any{
		"goal_parse_status": result.GoalParseStatus,
		"goal_status":       result.GoalStatus,
		"checklist_status":  result.ChecklistStatus,
		"goal_revision":     result.GoalRevision,
		"revision":          result.Revision,
		"open_items":        result.OpenItems,
		"observe_error":     result.ObserveError,
	})
}

// judgeInput is the wire shape of one done_when judge action, read as a
// JSON array from stdin rather than an argv flag: a judge's --reason text
// is arbitrary and may contain characters that would otherwise need
// careful shell quoting at the TOML call site.
type judgeInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func runResourceFinalize(args []string) error {
	fs := flag.NewFlagSet("resource finalize", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier (local-okf://<owner>/goals/<slug>.md)")
	revision := fs.String("revision", "", "revision being finalized")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resourceID == "" || *revision == "" {
		return fmt.Errorf("resource finalize requires --resource and --revision")
	}

	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read judges JSON from stdin: %w", err)
	}
	var judges []judgeInput
	if strings.TrimSpace(string(stdin)) != "" {
		if err := json.Unmarshal(stdin, &judges); err != nil {
			return fmt.Errorf("parse judges JSON from stdin: %w", err)
		}
	}
	goalJudges := make([]goal.Judge, len(judges))
	for i, j := range judges {
		goalJudges[i] = goal.Judge{ID: j.ID, Reason: j.Reason}
	}

	return resource.Finalize(cliexec.CLI{}, *resourceID, *revision, time.Now(), goalJudges)
}

func runProviderSetup(args []string) error {
	fs := flag.NewFlagSet("provider setup", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier (local-okf://<owner>/<concept-id>)")
	session := fs.String("session", "", "session name the workspace is acquired for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resourceID == "" || *session == "" {
		return fmt.Errorf("provider setup requires --resource and --session")
	}

	result, err := workspace.Setup(cliexec.CLI{}, *resourceID, *session)
	if err != nil {
		return err
	}

	return encodeJSON(os.Stdout, map[string]any{
		"workdir":      result.Workdir,
		"owner":        result.Owner,
		"concept_id":   result.ConceptID,
		"concept_path": result.ConceptPath,
	})
}

func runProviderCleanup(args []string) error {
	fs := flag.NewFlagSet("provider cleanup", flag.ContinueOnError)
	workdir := fs.String("workdir", "", "workdir recorded by provider setup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return workspace.Cleanup(*workdir)
}

func runTaskValidateGoalResource(args []string) error {
	fs := flag.NewFlagSet("task validate-goal-resource", flag.ContinueOnError)
	resourceID := fs.String("resource", "", "resource identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := task.ValidateGoalResource(*resourceID); err != nil {
		return err
	}
	return encodeJSON(os.Stdout, map[string]any{})
}

func runTaskBootstrap(args []string) error {
	fs := flag.NewFlagSet("task bootstrap", flag.ContinueOnError)
	workdirPath := fs.String("workdir", "", "orchestrator workdir path")
	owner := fs.String("owner", "", "owner alias goal resource ids are built with")
	session := fs.String("session", "", "session to instantiate pursue_goal on")
	assigneesJSON := fs.String("assignees", "", "JSON array of assignee strings to filter by (empty means no filter)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workdirPath == "" || *owner == "" || *session == "" {
		return fmt.Errorf("task bootstrap requires --workdir, --owner, and --session")
	}

	var assignees []string
	if strings.TrimSpace(*assigneesJSON) != "" {
		if err := json.Unmarshal([]byte(*assigneesJSON), &assignees); err != nil {
			return fmt.Errorf("parse --assignees JSON: %w", err)
		}
	}

	goalsDir := *workdirPath + "/knowledge/bundle/goals"
	created, err := task.Bootstrap(cliexec.CLI{}, goalsDir, *owner, *session, assignees)
	if err != nil {
		return err
	}

	return encodeJSON(os.Stdout, map[string]any{
		"created": strings.Join(created, ","),
	})
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}
