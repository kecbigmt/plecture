package commands

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// selectProjectRoot puts an unlisted project root to the person at the
// terminal; with no one to ask, the untrusted-root error stands. A session's
// own root is never put to them: trust is granted to a root found from the
// working directory, not to one a session recorded. The prompt goes to stderr
// so a confirmed run's stdout stays the command's own output.
func selectProjectRoot(cmd *cobra.Command, cfg *config.Config) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	if root, found, err := service.SessionProjectRoot(cfg, state.NewStore(""), dir); found || err != nil {
		return root, err
	}
	root, err := cfg.SelectProjectRoot(dir)
	var untrusted *config.UntrustedProjectError
	if !errors.As(err, &untrusted) || !isInteractive() {
		return root, err
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "%s is a project root that is not trusted.\nIts .plect/ definitions can run commands on this machine.\nTrust it and record it in trusted_project_roots? [y/N] ", untrusted.Root)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return "", fmt.Errorf("project root %s was not trusted", untrusted.Root)
	}
	path := config.DefaultPath()
	if path == "" {
		return "", errors.New("cannot locate config.toml to record the trusted project root")
	}
	if err := config.AddTrustedProjectRoot(path, untrusted.Root); err != nil {
		return "", fmt.Errorf("record trusted project root: %w", err)
	}
	cfg.TrustedProjectRoots = append(cfg.TrustedProjectRoots, untrusted.Root)
	return untrusted.Root, nil
}
