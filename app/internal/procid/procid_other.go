//go:build !linux && !darwin

package procid

import (
	"fmt"
	"runtime"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

const startTolerance = 0

func capture(pid int) (contract.ProcessIdentity, error) {
	return contract.ProcessIdentity{}, fmt.Errorf("process identity is not supported on %s", runtime.GOOS)
}
