package lang

import (
	"fmt"
	"regexp"
	"strings"
)

// MatchName is the resolver pair shared by every declaration that recognizes
// an identifier with `match` and derives a name from its captures with `name`.
// It is pure: one regular-expression match and one value resolution, no I/O.
// A non-matching id is not an error, so a caller can try several candidates.
func MatchName(match string, name *Value, id string) (resolved string, matched bool, err error) {
	re, err := regexp.Compile(match)
	if err != nil {
		return "", false, fmt.Errorf("match: %w", err)
	}
	m := re.FindStringSubmatch(id)
	if m == nil {
		return "", false, nil
	}
	captures := map[string]any{}
	for i, group := range re.SubexpNames() {
		if group != "" && i < len(m) {
			captures[group] = m[i]
		}
	}
	eval := Eval{Roots: Roots{"match": captures}}
	value, _, err := eval.Argument(name)
	if err != nil {
		return "", false, fmt.Errorf("name: %w", err)
	}
	resolved = strings.TrimSpace(value)
	if resolved == "" {
		return "", false, fmt.Errorf("name produced an empty value for %q", id)
	}
	return resolved, true, nil
}
