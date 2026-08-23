package effect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// Capabilities is what resolving one action needs beyond its surface roots:
// a plugin-scoped executable resolver, and — only for a terminal-capable
// surface — a resolver for {{terminal}} verbs. A caller that builds
// Capabilities owns its own session/task-DAG context; this package never
// sees that context, only the two closures derived from it.
type Capabilities struct {
	Bin func(ref string) (string, error)
	// Terminal resolves one terminal verb into the shell command a
	// consuming script runs. dir is the private run directory Resolve
	// materializes for this resolution, threaded through so a
	// materialized verb lives exactly as long as the execution consuming
	// it. Nil when the surface offers no terminal capability.
	Terminal func(dir, verb string) (string, error)
}

// Eval builds the lang.Eval one resolution runs against, wiring dir into the
// Terminal capability so a materialized verb's script lives in the same
// directory as the resolution consuming it. Exported so a caller that needs
// a bare lang.Eval (rather than a full Resolve) does not have to re-derive
// this wiring itself.
func (c Capabilities) Eval(roots lang.Roots, dir string) lang.Eval {
	e := lang.Eval{Roots: roots, Bin: c.Bin}
	if c.Terminal != nil {
		e.Terminal = func(verb string) (string, error) { return c.Terminal(dir, verb) }
	}
	return e
}

// Execution is one resolved action: the process it runs, plus the private
// run directory that process depends on — a shell action's binding file and
// any terminal verb the action consumes live there, so the directory has to
// outlive the resolution and not the process.
type Execution struct {
	execution *lang.Execution
	dir       string
}

// Resolve resolves one effect action against its surface roots and
// capabilities. Resolution is separate from running so a value that cannot
// be resolved is reported as the configuration error it is rather than as a
// failed execution.
func Resolve(action *lang.Action, roots lang.Roots, caps Capabilities, operands []string) (*Execution, error) {
	dir, err := os.MkdirTemp("", "plect-effect-")
	if err != nil {
		return nil, err
	}
	execution, err := caps.Eval(roots, dir).Run(filepath.Join(dir, "action"), action, operands)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &Execution{execution: execution, dir: dir}, nil
}

// Run executes the resolved process through the swappable executor every
// in-session execution takes.
func (e *Execution) Run(goCtx context.Context, workDir string, processEnv ...string) (stdout, stderr []byte, err error) {
	return ExecHook(goCtx, e.execution, workDir, processEnv...)
}

// Argv is the resolved process's argv, exposed for a test that asserts on
// what a value resolved to (or on the on-disk shell binding transport a
// shell action's argv path names) without shelling out.
func (e *Execution) Argv() []string {
	return e.execution.Argv
}

func (e *Execution) Close() {
	if e != nil {
		os.RemoveAll(e.dir)
	}
}

// ResolveValues resolves one value table — a nesting joint's inputs or
// environment — into the strings the next layer inward receives. Keys are
// walked in order so a diagnostic and a recorded execution are reproducible
// rather than map-ordered.
func ResolveValues(values map[string]*lang.Value, roots lang.Roots, caps Capabilities) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	// A joint value reaches no terminal verb, so the run directory Resolve
	// otherwise materializes one into is never created here.
	eval := caps.Eval(roots, "")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(values))
	for _, key := range keys {
		resolved, absent, err := eval.Argument(values[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if absent {
			continue
		}
		out[key] = resolved
	}
	return out, nil
}
