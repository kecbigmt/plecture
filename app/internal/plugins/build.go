package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

// BuildOutputPaths returns the dir-relative executable paths RunBuilds will
// write to: every executables[] entry with a non-empty build command. These
// are excluded from the plugin's content hash (HashTreeExcluding) — see its
// doc comment for why.
func BuildOutputPaths(m Manifest) []string {
	var out []string
	for _, ex := range m.Executables {
		if ex.Build != "" {
			out = append(out, ex.Path)
		}
	}
	return out
}

// RunBuilds executes each executable's declared `build` command inside dir
// (the resolved plugin directory), in manifest order, and verifies the
// command produced the file at Path. Build-less entries are skipped without
// error — they are the primary v1 form (a script already present in the
// source tree) and need no compilation step. Running a build command is
// within the trust already granted by confirming the source: plugin config
// can run shell during normal operation regardless.
func RunBuilds(ctx context.Context, runner procexec.Runner, dir string, m Manifest) error {
	for _, ex := range m.Executables {
		if ex.Build == "" {
			continue
		}
		if _, _, err := runner.Run(ctx, dir, false, "sh", "-c", ex.Build); err != nil {
			return fmt.Errorf("plugin executable %q: build %q: %w", ex.Name, ex.Build, err)
		}
		if _, err := os.Stat(filepath.Join(dir, ex.Path)); err != nil {
			return fmt.Errorf("plugin executable %q: build %q did not produce %s: %w", ex.Name, ex.Build, ex.Path, err)
		}
	}
	return nil
}
