// Package version holds the running plect binary's version string.
package version

// Current is the running plect version, compared against a plugin's
// declared plect_min_version. It is a hardcoded placeholder because plect
// has no release/tagging process yet; wire this to build-time injection
// (ldflags) once one exists, without changing any caller of Current.
const Current = "0.0.0-dev"
