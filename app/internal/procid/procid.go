// Package procid captures and compares operating-system process identity: a
// pid together with the numeric start time the platform reports for it. A pid
// alone is not an identity, because the kernel recycles it; the start time is
// what tells a still-running process apart from an unrelated later one that
// received the same pid.
package procid

import (
	"errors"
	"fmt"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

var (
	// ErrNotRunning means no live process holds the pid. A zombie counts as
	// not running: it has exited and only awaits its parent's wait.
	ErrNotRunning = errors.New("process is not running")
	// ErrReused means a live process holds the pid but is not the one that was
	// recorded.
	ErrReused = errors.New("pid is held by a different process than the one recorded")
)

// Capture reads the identity of the live process holding pid.
func Capture(pid int) (contract.ProcessIdentity, error) {
	if pid <= 0 {
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d is not a process id", pid)
	}
	return capture(pid)
}

// Verify reports whether the process holding recorded.PID is still the one
// recorded: nil when it is, ErrNotRunning when nothing live holds the pid, and
// ErrReused when something else does.
func Verify(recorded contract.ProcessIdentity) error {
	current, err := Capture(recorded.PID)
	if err != nil {
		return err
	}
	if current.Boot != recorded.Boot || abs(current.Start-recorded.Start) > startTolerance {
		return fmt.Errorf("pid %d: %w", recorded.PID, ErrReused)
	}
	return nil
}

// VerifyPIDOnly is the judgment for a record that carries no start time: the
// pid's existence is all there is to check.
func VerifyPIDOnly(pid int) error {
	_, err := Capture(pid)
	return err
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
