package commands

import (
	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/procid"
)

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Inspect a process core recorded",
}

var processVerifyCmd = &cobra.Command{
	Use:   "verify <identity>",
	Short: "Exit zero only if the recorded process is still running under its pid",
	Long: "Check an identity token, as an effect's cleanup receives it from `self.process.identity`, against the live process table.\n" +
		"Exit status is zero when the pid is held by the recorded process, and non-zero when nothing holds it or another process does.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return verifyProcessIdentity(args[0])
	},
}

func verifyProcessIdentity(token string) error {
	id, err := procid.Decode(token)
	if err != nil {
		return err
	}
	return procid.Verify(id)
}

func init() {
	processCmd.AddCommand(processVerifyCmd)
	rootCmd.AddCommand(processCmd)
}
