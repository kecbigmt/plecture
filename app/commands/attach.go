package commands

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var attachCmd = &cobra.Command{
	Use:     "attach <url|session>",
	Aliases: []string{"a"},
	Short:   "Attach to the workflow's declared attach target",
	Long: `Resolve the session, find the task declaring 'attach', and exec into
its runtime — the plect process is replaced so the TTY is fully handed over.

No auto-up: a session whose attach task is not 'produced' aborts with a
hint to run 'plect up <session>' first. Compose with the shell when you want
both: 'plect up <name> && plect attach <name>'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		store := state.NewStore("")
		result, err := service.Attach(cfg, store, service.AttachParams{Identifier: args[0]})
		if err != nil {
			return err
		}
		// Resolve via $PATH so users get the shell's view of the binary,
		// matching how the rendered command would behave under `bash -c`.
		bash, err := exec.LookPath("bash")
		if err != nil {
			return fmt.Errorf("bash not found in PATH: %w", err)
		}
		return syscall.Exec(bash, []string{"bash", "-c", result.Command}, os.Environ())
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}
