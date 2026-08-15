package plugins

import (
	"fmt"
	"strings"
)

// Scheme identifies which resolver handles a registered catalog source.
type Scheme int

const (
	SchemeUnknown Scheme = iota
	SchemeGitHTTPS
	SchemeGitSSH
	SchemePath
	// SchemePathEditable is path's explicit local-development counterpart: a
	// distinct scheme, not a boolean flag on the registration, so an
	// editable catalog reads as such directly from its source string.
	SchemePathEditable
)

// ParseSource classifies source by its v1 scheme prefix and returns the
// scheme plus the remainder used by that scheme's resolver: for the git
// schemes, the transport URL git itself understands (https://... or
// ssh://...); for the path schemes, the filesystem path.
func ParseSource(source string) (Scheme, string, error) {
	switch {
	case strings.HasPrefix(source, "git+https://"):
		return SchemeGitHTTPS, strings.TrimPrefix(source, "git+"), nil
	case strings.HasPrefix(source, "git+ssh://"):
		return SchemeGitSSH, strings.TrimPrefix(source, "git+"), nil
	case strings.HasPrefix(source, "path+editable://"):
		return SchemePathEditable, strings.TrimPrefix(source, "path+editable://"), nil
	case strings.HasPrefix(source, "path://"):
		return SchemePath, strings.TrimPrefix(source, "path://"), nil
	default:
		return SchemeUnknown, "", fmt.Errorf("source %q: unsupported scheme (want git+https://, git+ssh://, path://, or path+editable://)", source)
	}
}

// IsGit reports whether scheme resolves through git.
func (s Scheme) IsGit() bool {
	return s == SchemeGitHTTPS || s == SchemeGitSSH
}

// IsEditable reports whether scheme is the explicit local-development
// escape hatch: mounted directly, never cached, never content-hash pinned.
func (s Scheme) IsEditable() bool {
	return s == SchemePathEditable
}
