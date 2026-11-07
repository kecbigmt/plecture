package state

import (
	"strings"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

func testResource(locator string) *contract.Resource {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, locator)
	return &contract.Resource{Type: "test", Name: "r" + name, Locator: locator}
}
