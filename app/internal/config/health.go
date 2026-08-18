package config

import "fmt"

// HealthConfig is a task's `[health]` table: how this task's health is
// determined. The two probes are independent — a task may declare either,
// both, or (by omitting the table) neither.
//
//	[health]
//	alive    = "kill -0 {{.Self.pid}}"
//	activity = '{{bin "claude-agent-activity"}} probe {{.SessionName}}'
//
// Alive is the liveness probe: exit-code semantics, zero means the execution
// surface is present. Activity is the activity probe: it writes a JSON
// activity envelope on stdout whose opaque fingerprint core compares across
// evaluations. A readiness-style third probe is deliberately absent — health
// answers "is this surface present and moving", not "may traffic be sent".
type HealthConfig struct {
	Alive    string `toml:"alive"`
	Activity string `toml:"activity"`
}

// Validate rejects a `[health]` table that declares nothing. Unlike
// `[terminal]`, the members carry no all-or-nothing obligation on each other:
// a process task may have a meaningful liveness probe and no activity to
// fingerprint, and a fingerprint-only task (no process of its own to probe)
// is equally legitimate. A bare header is still an error rather than an inert
// declaration, because the only reason to write `[health]` is to declare a
// probe. A nil receiver (no table at all) is valid.
func (h *HealthConfig) Validate() error {
	if h == nil {
		return nil
	}
	if h.Alive == "" && h.Activity == "" {
		return fmt.Errorf("[health] table declares no probe (want alive, activity, or both)")
	}
	return nil
}

// AliveProbe returns the declared liveness probe command, or "" when none is
// declared. Nil-safe so callers holding an optional table need no guard.
func (h *HealthConfig) AliveProbe() string {
	if h == nil {
		return ""
	}
	return h.Alive
}

// ActivityProbe returns the declared activity probe command, or "" when none
// is declared. Nil-safe, mirroring AliveProbe.
func (h *HealthConfig) ActivityProbe() string {
	if h == nil {
		return ""
	}
	return h.Activity
}
