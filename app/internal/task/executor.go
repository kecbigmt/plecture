package task

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kecbigmt/plecture/app/internal/lang"
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

// Executor runs an ExecRequest and returns its captured stdout/stderr. The
// only implementation is the host one (see hostExecutor); the seam exists so
// tests can observe what each exec path issues. ctx governs the lifetime of
// the underlying child process: a cancelled ctx must terminate it and surface
// an error, not merely stop waiting on it.
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

// alwaysHostExecutor backs runShell, the path used by workspace provider
// setup/cleanup, workspace provider subscribe, and resource observe/finalize.
// Unlike defaultExecutor, this is never swapped, not even by tests.
var alwaysHostExecutor Executor = hostExecutor{}

// defaultExecutor backs execHostScript, the path used by task
// setup/cleanup/health probes/capture, dynamic output fetch, and dynamic
// instance setup (`plect task setup --resource`) — every exec point that runs
// inside a session's task DAG, static or dynamically instantiated. Tests swap
// it for a spy (see executor_test.go) to observe the ExecRequest each path
// issues without changing any exported function signature.
var defaultExecutor Executor = hostExecutor{}

// requestFor is the one place a resolved lifecycle execution becomes a host
// invocation, whichever declaration form produced it. A ratified action
// arrives already resolved into its argv and standard input; a
// template-rendered hook arrives as shell source and is run through
// `bash -c`, with no standard input, because that is what it has always
// meant.
func requestFor(execution *lang.Execution, workDir string, env []string) ExecRequest {
	return ExecRequest{Argv: execution.Argv, Stdin: execution.Stdin, Dir: workDir, Env: env}
}

// renderedShell wraps one template-rendered hook as the process that runs
// it, so a converted surface and a legacy one reach the executor by the same
// path and differ only in how they produced their Execution.
//
// Retirement: the surface reshape replaces its callers one at a time with
// lang.Eval; the PR that converts the last template-rendered surface deletes
// this function, execHostScript, and runShell along with it.
func renderedShell(script string) *lang.Execution {
	return &lang.Execution{Argv: []string{"bash", "-c", script}}
}

// execHook runs one resolved execution through the swappable
// defaultExecutor rather than the pinned alwaysHostExecutor — see the two
// vars' docs for why the distinction matters. env carries the KEY=VALUE
// additions the enclosing layers of a nesting chain inject into this
// execution; it is empty for every plain task.
func execHook(ctx context.Context, execution *lang.Execution, workDir string, env ...string) (stdout, stderr []byte, err error) {
	return defaultExecutor.Run(ctx, requestFor(execution, workDir, env))
}

// runHook runs one resolved execution on the host, unconditionally: the path
// workspace provider setup/cleanup, provider subscribe, and resource
// observe/finalize take.
func runHook(ctx context.Context, execution *lang.Execution, workDir string) (stdout, stderr []byte, err error) {
	return alwaysHostExecutor.Run(ctx, requestFor(execution, workDir, nil))
}

func execHostScript(ctx context.Context, cmdStr, workDir string, env ...string) (stdout, stderr []byte, err error) {
	return execHook(ctx, renderedShell(cmdStr), workDir, env...)
}
