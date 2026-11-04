// Package procid captures and compares process identity: a pid together with
// its start time, because the kernel recycles pids.
package procid

import (
	"encoding/json"
	"errors"
	"fmt"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

var (
	ErrNotRunning = errors.New("process is not running")
	ErrReused     = errors.New("pid is held by a different process than the one recorded")
)

func Capture(pid int) (contract.ProcessIdentity, error) {
	if pid <= 0 {
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d is not a process id", pid)
	}
	return capture(pid)
}

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

// Encode renders an identity as the opaque token `plect process verify`
// accepts, so a script can hand it back to core for a fresh check.
func Encode(id contract.ProcessIdentity) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}

func Decode(token string) (contract.ProcessIdentity, error) {
	var id contract.ProcessIdentity
	if err := json.Unmarshal([]byte(token), &id); err != nil {
		return id, fmt.Errorf("not a process identity: %w", err)
	}
	if id.PID <= 0 {
		return id, fmt.Errorf("not a process identity: pid %d", id.PID)
	}
	return id, nil
}
