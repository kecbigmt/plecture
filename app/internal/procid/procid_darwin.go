//go:build darwin

package procid

import (
	"errors"
	"fmt"
	"syscall"

	contract "github.com/kecbigmt/plecture/contracts/state"
	"golang.org/x/sys/unix"
)

// The tolerance is zero because kinfo_proc records an absolute microsecond
// timestamp that nothing rounds between reads.
const startTolerance = 0

const szomb = 5

func capture(pid int) (contract.ProcessIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// The kernel answers an unheld pid with an empty record, which the
		// sysctl wrapper reports as EIO.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EIO) {
			return contract.ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNotRunning)
		}
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d: read kern.proc.pid: %w", pid, err)
	}
	if info.Proc.P_stat == szomb {
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNotRunning)
	}
	started := info.Proc.P_starttime
	return contract.ProcessIdentity{PID: pid, Start: int64(started.Sec)*1_000_000 + int64(started.Usec)}, nil
}
