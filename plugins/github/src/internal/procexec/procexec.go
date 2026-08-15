// Package procexec provides a context-aware seam for running external host
// processes so that callers who cancel or time out their context reliably
// terminate the child rather than leaking it.
package procexec

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Runner starts name with args in dir and returns its captured stdout/stderr.
// When mirror is true, both streams are also copied live to os.Stderr, for
// callers that want the user to see progress/errors as they happen. ctx
// governs the child's lifetime: a cancelled or expired ctx terminates the
// process and Run returns a non-nil error.
type Runner interface {
	Run(ctx context.Context, dir string, mirror bool, name string, args ...string) (stdout, stderr []byte, err error)
}

// Host runs processes directly on the current host.
type Host struct{}

// Run implements Runner.
func (Host) Run(ctx context.Context, dir string, mirror bool, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var outBuf, errBuf bytes.Buffer
	if mirror {
		cmd.Stdout = io.MultiWriter(os.Stderr, &outBuf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	} else {
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
	}

	// Put the child in its own process group and, on cancellation, kill the
	// whole group rather than just the direct child. A command may spawn its
	// own helper processes (e.g. credential prompts, pagers) that would
	// otherwise keep the stdout/stderr pipes open and block cmd.Wait even
	// after the direct child is gone. WaitDelay bounds how long Wait keeps
	// waiting after Cancel runs before forcibly closing the I/O pipes, so a
	// runaway grandchild can't hang the call forever even if the group kill
	// somehow fails to reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// Default is the process-wide Host runner. Tests may swap in a fake binary
// via PATH rather than replacing this, since Host's behavior (process-group
// kill, WaitDelay) is itself part of what needs covering.
var Default Runner = Host{}
