package provider

import (
	"context"
	"fmt"

	"github.com/kecbigmt/plecture/plugins/github-provider/internal/procexec"
)

// defaultRunner shells out to the real plecture CLI. Stdout is the JSON the
// workspace subcommands print, so it is captured rather than streamed; a
// failure carries the child's stderr into the returned error, which is where
// the caller surfaces it.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, stderr, err := procexec.Default.Run(ctx, "", false, name, args...)
	if err != nil {
		if len(stderr) > 0 {
			return nil, fmt.Errorf("%s %v: %w: %s", name, args, err, string(stderr))
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return stdout, nil
}
