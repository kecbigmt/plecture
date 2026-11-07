package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

var (
	destroyForce         bool
	destroyRetiredInputs []string
)

var destroyCmd = &cobra.Command{
	Use:   "destroy <resource-id|session>",
	Short: "Tear down a session via task cleanup",
	Long: `Tear down a session in three ordered steps:

  1. run-scoped cleanup (auto-down) — skipped if no run-scoped task is
     currently in "produced" status
  2. session-scoped cleanup
  3. state entry deletion

By default destroy stops at the first safeguard or error and keeps the state
entry so you can inspect and retry: it refuses a session that has child
sessions (deleting it would orphan them; plect up never re-adopts an orphan)
or that has no stored workflow revision, and a failed cleanup aborts the
remaining steps while the prerequisites of the failed node stay in place.
To reset a session that has children without orphaning them, use
` + "`plect down`" + ` + ` + "`plect up`" + ` instead.

--force ignores those safeguards and errors and runs destroy to the end:

  - child sessions are orphaned (reported as a warning)
  - a session with no stored workflow revision is removed without cleanup
  - a failed cleanup is demoted to a warning and the state entry is deleted
    without verifying the release
  - the prerequisites of a node whose cleanup failed are released too
  - each effect's cleanup receives force = true, telling it to override its
    own safeguards as well, for example by discarding uncommitted changes in
    a directory it created

Whatever those safeguards protected — uncommitted changes, child sessions,
an allocation nobody verified was released — can be lost.

What a cleanup releases is decided when the session is created, through the
effect's inputs; destroy takes no per-call plugin inputs. To change that
choice for one session, rewrite the effect's mutable output with
` + "`plect state set-output`" + ` before destroying it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("input") {
			return fmt.Errorf("--input is retired: what a cleanup releases is decided when the session is created, and an effect's mutable output can be rewritten with `plect state set-output` before destroy; see %s", lang.WorkspaceProviderMigrationGuide)
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		store := state.NewStore("")
		result, err := service.Destroy(cfg, store, service.DestroyParams{
			Identifier: args[0],
			Force:      destroyForce,
			Observer:   newTaskObserver(cfg),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Destroyed session: %s\n", result.SessionName)
		for _, w := range result.CleanupWarnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		return nil
	},
}

func init() {
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Ignore destroy's safeguards and errors and run it to the end: orphan child sessions, delete the state entry even when cleanup fails, and pass force = true to each effect's cleanup, which may discard uncommitted changes")
	// The flag is kept, hidden, only so that a script still passing it is
	// pointed at the migration guide instead of told the flag is unknown.
	destroyCmd.Flags().StringArrayVar(&destroyRetiredInputs, "input", nil, "retired")
	_ = destroyCmd.Flags().MarkHidden("input")
	rootCmd.AddCommand(destroyCmd)
}
