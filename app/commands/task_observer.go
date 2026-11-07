package commands

import (
	"os"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// We seed the id column from the *global* task definitions only — repoDir
// hasn't been resolved at command setup time. Per-repo overrides are rare and
// would only widen the column, so the global layer is a good-enough alignment
// seed.
func newTaskObserver(cfg *config.Config) task.Observer {
	defs, _ := cfg.LoadTaskDefinitions("")
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	return task.NewStreamReporter(os.Stderr, ids)
}
