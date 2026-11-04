//go:build linux

package procid

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// startTolerance is one clock tick: the kernel reports the start time in
// whole ticks, and a probe reading it back must not call a process different
// over a rounding step.
const startTolerance = 1

// The start time stays in ticks since boot, paired with the boot id, rather
// than being converted to wall-clock time with the boot time: that conversion
// shifts whenever the system clock is stepped, and a false "different
// process" verdict recreates a live agent.
func capture(pid int) (contract.ProcessIdentity, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return contract.ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNotRunning)
		}
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d: read process stat: %w", pid, err)
	}
	state, start, err := parseStat(string(raw))
	if err != nil {
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, err)
	}
	if state == 'Z' || state == 'X' {
		return contract.ProcessIdentity{}, fmt.Errorf("pid %d: %w", pid, ErrNotRunning)
	}
	boot, err := bootID()
	if err != nil {
		return contract.ProcessIdentity{}, err
	}
	return contract.ProcessIdentity{PID: pid, Start: start, Boot: boot}, nil
}

// parseStat reads the state and start time out of /proc/<pid>/stat. The
// command name (field 2) is parenthesized and may itself contain spaces and
// parentheses, so fields are counted from the last closing parenthesis.
func parseStat(stat string) (state byte, start int64, err error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, 0, fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is stat field 3 (state); starttime is stat field 22.
	const startIndex = 22 - 3
	if len(fields) <= startIndex || len(fields[0]) != 1 {
		return 0, 0, fmt.Errorf("malformed process stat")
	}
	start, err = strconv.ParseInt(fields[startIndex], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed process start time %q", fields[startIndex])
	}
	return fields[0][0], start, nil
}

func bootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read boot id: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}
