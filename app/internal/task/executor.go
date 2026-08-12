package task

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/plecture/plect/app/internal/config"
)

// ExecRequest is a single host-process invocation: Argv[0] is the command,
// Dir is the working directory (applied only if it exists, see hostExecutor),
// and Stdin/Env are optional (nil means "none" / "inherit the process env",
// matching what runShell has always done implicitly).
type ExecRequest struct {
	Argv  []string
	Dir   string
	Stdin []byte
	Env   []string
}

// Executor runs an ExecRequest and returns its captured stdout/stderr. It is
// the seam an environment-aware dispatch (docker exec, etc.) plugs into; the
// only implementation today is the host one, which reproduces runShell's
// exact semantics byte-for-byte (see hostExecutor). ctx governs the lifetime
// of the underlying child process: a cancelled ctx must terminate it and
// surface an error, not merely stop waiting on it.
type Executor interface {
	Run(ctx context.Context, req ExecRequest) (stdout, stderr []byte, err error)
}

// hostExecutor runs argv directly as a host process — the extraction of what
// runShell has always done (bash -c under the hood, for the two callers
// below; argv directly for everyone else).
type hostExecutor struct{}

func (hostExecutor) Run(ctx context.Context, req ExecRequest) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if req.Dir != "" {
		if _, statErr := os.Stat(req.Dir); statErr == nil {
			cmd.Dir = req.Dir
		}
	}
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}
	// Put the child in its own process group and, on cancellation, kill the
	// whole group rather than just the direct child. A shell script's own
	// children (e.g. "sleep 5" spawned by "bash -c") don't die with their
	// parent: exec.CommandContext's default Cancel only signals the direct
	// child, so a grandchild holding the stdout/stderr pipes open would keep
	// cmd.Wait blocked until it exits on its own, defeating cancellation
	// entirely. WaitDelay bounds how long Wait keeps waiting after Cancel runs before
	// forcibly closing the I/O pipes, so a runaway grandchild can't hang the
	// call forever even if the group kill somehow fails to reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// alwaysHostExecutor backs runShell, the path used by provider setup/cleanup,
// provider subscribe, and resource observe/finalize. Those must always run on
// the host regardless of any workflow's Environment — provider setup runs
// before any environment could exist, and standalone resource commands have
// no session (and so no Environment) to consult at all. Unlike
// defaultExecutor, this is never swapped, not even by tests.
var alwaysHostExecutor Executor = hostExecutor{}

// defaultExecutor backs execHostScript, the path used by task
// setup/cleanup/healthcheck/capture, dynamic output fetch, and dynamic
// instance setup (`plect task setup --resource`) — every exec point that runs
// inside a session's task DAG, static or dynamically instantiated. It is the
// seam a later PR will make Environment-aware; today it is always
// hostExecutor, so behavior is identical to before this abstraction existed.
// Tests swap it for a spy (see executor_test.go) to observe the ExecRequest
// each path issues without changing any exported function signature.
var defaultExecutor Executor = hostExecutor{}

// execHostScript renders down to the same host semantics as runShell, but
// through the swappable defaultExecutor rather than the pinned
// alwaysHostExecutor — see the two vars' docs for why the distinction matters.
func execHostScript(ctx context.Context, cmdStr, workDir string) (stdout, stderr []byte, err error) {
	return defaultExecutor.Run(ctx, ExecRequest{Argv: []string{"bash", "-c", cmdStr}, Dir: workDir})
}

// EnvironmentExecutor routes an ExecRequest through an environment's `exec`
// script instead of running the target argv directly. The exec script itself
// always runs on the host (alwaysHostExecutor) — it is what invokes `docker
// exec` or equivalent — and the target argv is appended as trailing
// positional parameters, not interpolated into the rendered command string,
// so the environment's own script decides how to forward it (typically
// `"$@"`) rather than re-parsing a shell string that could contain
// metacharacters belonging to the target command.
type EnvironmentExecutor struct {
	Env     config.EnvironmentConfig
	Outputs map[string]any
}

// NewEnvironmentExecutor builds an EnvironmentExecutor for env, exposing its
// setup outputs to the exec script as PLECT_ENV_* environment variables.
func NewEnvironmentExecutor(env config.EnvironmentConfig, outputs map[string]any) *EnvironmentExecutor {
	return &EnvironmentExecutor{Env: env, Outputs: outputs}
}

func (e *EnvironmentExecutor) Run(ctx context.Context, req ExecRequest) (stdout, stderr []byte, err error) {
	// "plect-env-exec" becomes $0 inside the exec script (bash -c's first
	// trailing arg), so req.Argv is exactly what the script's "$@" expands to.
	argv := append([]string{"bash", "-c", e.Env.Exec, "plect-env-exec"}, req.Argv...)
	env := append(environmentExecEnv(e.Env.ID, e.Outputs), req.Env...)
	return alwaysHostExecutor.Run(ctx, ExecRequest{Argv: argv, Dir: req.Dir, Stdin: req.Stdin, Env: env})
}

// environmentExecEnv exposes the environment id and its setup outputs' string
// values as PLECT_ENV_* shell variables (e.g. `docker exec -w
// "$PLECT_ENV_WORKDIR" "$PLECT_ENV_ID" "$@"`) rather than Go template holes: exec
// runs once per task invocation and the outputs are stable for the session's
// whole lifetime, so there is no per-call templating to do.
func environmentExecEnv(id string, outputs map[string]any) []string {
	env := []string{"PLECT_ENV_ID=" + id}
	for k, v := range outputs {
		s, ok := v.(string)
		if !ok {
			continue
		}
		env = append(env, "PLECT_ENV_"+strings.ToUpper(k)+"="+s)
	}
	return env
}
