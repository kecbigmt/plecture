package effect

import (
	"context"
	"os"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// HookEval resolves a resource hook's values, with `bin` resolving
// against the plugin that declared it.
func HookEval(env lang.Roots, mounted []plugins.Mounted, sourcePath string, from lang.Ownership) lang.Eval {
	bins := config.MountedBins{Mounted: mounted, SourcePath: sourcePath}
	return lang.Eval{
		Roots: env,
		Bin:   func(ref string) (string, error) { return bins.ResolveBin(ref, from) },
	}
}

// RunHookAction resolves one resource hook and runs it on the host. A
// shell action gets a private run directory for the binding transport,
// created only for that variant.
func RunHookAction(action *lang.Action, eval lang.Eval) (stdout, stderr []byte, err error) {
	runDir := ""
	if action.Type == lang.ActionShell {
		dir, mkErr := os.MkdirTemp("", "plect-provider-")
		if mkErr != nil {
			return nil, nil, mkErr
		}
		defer os.RemoveAll(dir)
		runDir = dir
	}
	execution, err := eval.Run(runDir, action, nil)
	if err != nil {
		return nil, nil, err
	}
	// A resource hook runs before and after any session directory exists, so
	// it runs from the caller's cwd and must use absolute paths.
	return RunHook(context.Background(), execution, "")
}
