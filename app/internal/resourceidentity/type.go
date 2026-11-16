package resourceidentity

import (
	"strings"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// Legacy bare types cannot identify a catalog, so only the same locator may
// reuse one under a qualified address.
func SameRecordedType(recorded, requested contract.Resource) bool {
	if recorded.Type == requested.Type {
		return true
	}
	return !strings.Contains(recorded.Type, ".") && strings.HasSuffix(requested.Type, "."+recorded.Type) && recorded.Locator == requested.Locator
}
