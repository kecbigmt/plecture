package population

import (
	"strings"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// sourceResource is the resource the population fixture's entry derives
// from a urn:case identifier.
func sourceResource(locator string) contract.Resource {
	return contract.Resource{Type: "source", Name: memberName(locator), Locator: locator}
}

func memberName(locator string) string {
	name := strings.TrimPrefix(locator, "urn:case:")
	if i := strings.Index(name, "#"); i >= 0 {
		name = name[:i]
	}
	return name
}

func sourceRef(locator string) *contract.Resource {
	r := sourceResource(locator)
	return &r
}
