package config

import (
	"fmt"
	"sort"
	"strings"
)

// AddressHint suggests the address a reference that resolved to nothing was
// probably reaching for. A plugin's declarations answer to their catalog
// address, so the likeliest reason a reference misses is that it names the id
// alone where the alias is required — and the id alone is what a reader who
// has only seen the plugin's own documentation would write.
//
// The hint is offered only when the bare id is unambiguous among the
// addresses on offer, or names them all when it is not: a hint that guesses
// between two plugins would send the reader to the wrong one.
func AddressHint(addresses []string, ref string) string {
	if strings.Contains(ref, ".") {
		return ""
	}
	var matches []string
	for _, address := range addresses {
		if strings.HasSuffix(address, "."+ref) {
			matches = append(matches, address)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("; an enabled plugin declares it as %q — a reference to a plugin's declaration carries its catalog address", matches[0])
	default:
		return fmt.Sprintf("; enabled plugins declare it as %s — a reference to a plugin's declaration carries its catalog address, and these are distinct declarations", strings.Join(quoteAll(matches), " and "))
	}
}

func quoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}

// Addresses lists a loaded map's keys, for an AddressHint on a failed lookup.
func Addresses[T any](defs map[string]T) []string {
	out := make([]string, 0, len(defs))
	for address := range defs {
		out = append(out, address)
	}
	return out
}
