package plugins

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

// FetchGit clones transportURL at revision into destDir (which must not
// already exist) and returns the resolved commit hash. It shells out to the
// git binary via runner rather than taking a git library dependency, so
// core's only git requirement is the binary being on PATH; environments
// without git lose only the git schemes.
func FetchGit(ctx context.Context, runner procexec.Runner, transportURL, revision, destDir string) (resolvedRevision string, err error) {
	if _, statErr := os.Stat(destDir); statErr == nil {
		return "", fmt.Errorf("fetch git source %s: destination %s already exists", transportURL, destDir)
	}
	if _, _, err := runner.Run(ctx, "", false, "git", "clone", "--quiet", transportURL, destDir); err != nil {
		return "", fmt.Errorf("git clone %s: %w", transportURL, err)
	}
	if _, _, err := runner.Run(ctx, destDir, false, "git", "checkout", "--quiet", revision); err != nil {
		return "", fmt.Errorf("git checkout %s at revision %q: %w", transportURL, revision, err)
	}
	out, _, err := runner.Run(ctx, destDir, false, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD for %s: %w", transportURL, err)
	}
	return strings.TrimSpace(string(out)), nil
}
