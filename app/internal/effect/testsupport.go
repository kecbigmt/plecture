package effect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// UseExecutor swaps the package's swappable executor for e for the
// duration of a test and returns a restore func. It is exported (rather
// than living in a _test.go file) so a consumer package's own tests can
// observe what ExecHook issues without this package exposing the
// swappable var itself.
func UseExecutor(e Executor) (restore func()) {
	orig := defaultExecutor
	defaultExecutor = e
	return func() { defaultExecutor = orig }
}

// ReadShellRun reads what a resolved shell action's process actually runs
// out of the run directory, while it still exists: the argv is a generated
// wrapper, so the script and the bindings are the only observable contract.
// An exec action's process has neither, and reports its own last argument
// instead — what a test naming one execution among several keys on.
func ReadShellRun(req ExecRequest) (script, bindings string) {
	if len(req.Argv) != 1 || !strings.HasSuffix(req.Argv[0], "run.sh") {
		return req.Argv[len(req.Argv)-1], ""
	}
	dir := filepath.Dir(req.Argv[0])
	if raw, err := os.ReadFile(filepath.Join(dir, "script.sh")); err == nil {
		script = string(raw)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "bindings.sh")); err == nil {
		bindings = string(raw)
	}
	return script, bindings
}

// SpyExecutor records every ExecRequest it receives and returns a canned
// result, so a test can assert on argv/cwd without shelling out.
type SpyExecutor struct {
	Requests []ExecRequest
	Scripts  []string
	Bindings []string
	Stdout   []byte
	Stderr   []byte
	Err      error
}

func (s *SpyExecutor) Run(_ context.Context, req ExecRequest) (stdout, stderr []byte, err error) {
	s.Requests = append(s.Requests, req)
	script, bindings := ReadShellRun(req)
	s.Scripts = append(s.Scripts, script)
	s.Bindings = append(s.Bindings, bindings)
	return s.Stdout, s.Stderr, s.Err
}
