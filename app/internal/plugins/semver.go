package plugins

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a parsed MAJOR.MINOR.PATCH triple. Plugin compatibility only
// needs ordering between plect_min_version and the running plect version, so
// this intentionally does not implement full SemVer 2.0 precedence for
// pre-release/build-metadata suffixes: it strips a trailing "-suffix" (used
// by version.Current's "0.0.0-dev" placeholder) and compares the numeric
// core only.
type semver struct {
	major, minor, patch int
}

func parseSemver(s string) (semver, error) {
	orig := s
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("version %q: want MAJOR.MINOR.PATCH", orig)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("version %q: component %q is not a non-negative integer", orig, p)
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2]}, nil
}

// compareSemver returns -1, 0, or 1 as a is less than, equal to, or greater
// than b.
func compareSemver(a, b semver) int {
	switch {
	case a.major != b.major:
		return cmpInt(a.major, b.major)
	case a.minor != b.minor:
		return cmpInt(a.minor, b.minor)
	default:
		return cmpInt(a.patch, b.patch)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether running satisfies minVersion (running >=
// minVersion). Both must parse as MAJOR.MINOR.PATCH.
func AtLeast(running, minVersion string) (bool, error) {
	r, err := parseSemver(running)
	if err != nil {
		return false, err
	}
	m, err := parseSemver(minVersion)
	if err != nil {
		return false, err
	}
	return compareSemver(r, m) >= 0, nil
}
