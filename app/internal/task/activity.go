package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ActivitySignal is the opaque, provider-neutral fact a declared activity
// probe reports about one task instance. Core never interprets what the probe
// actually observed (a terminal pane, an agent's turn boundary, a VCS
// workspace, ...) — it only compares Fingerprint and ObservedAt across
// evaluations and reads Supported/ActivityExpected as plain booleans.
type ActivitySignal struct {
	// Supported reports whether this instance currently has a basis to
	// judge activity at all. A probe may run successfully and still
	// report false — an explicit "nothing to say right now" declaration,
	// distinct from no probe being declared.
	Supported bool
	// ActivityExpected is the probe's own contribution to whether activity
	// is currently expected of the declaring instance (e.g. "the turn
	// already ended"). Core combines this with its own done_when-derived
	// expectation; a probe can narrow that expectation but never manufacture
	// one core's done_when/task-state evaluation does not already see.
	ActivityExpected bool
	// Fingerprint is an opaque token that changes whenever the probe
	// observes new activity. Core never parses it, only compares it.
	Fingerprint string
	// ObservedAt is when the probe captured this fact. A zero value means
	// the probe did not report a timestamp.
	ObservedAt time.Time
}

// activitySignalWire is the JSON envelope an activity probe writes to stdout.
// Both booleans are pointers so an omitted field defaults to true: a probe
// that reports facts without bothering to declare "supported" is implicitly
// declaring support, and a probe with no opinion on whether activity is
// expected must not silently narrow the expectation core derived from
// done_when — only an explicit `false` may do that.
type activitySignalWire struct {
	Supported        *bool  `json:"supported"`
	ActivityExpected *bool  `json:"activity_expected"`
	Fingerprint      string `json:"fingerprint"`
	ObservedAt       string `json:"observed_at"`
}

// RunActivityProbe renders cmd against the task's own outputs, its resolved
// node inputs, and session vars (mirroring RunAliveProbe), runs it via
// bash -c, and parses its stdout as an ActivitySignal. A non-zero exit
// or a render failure is returned as an error carrying stderr, mirroring
// RunAliveProbe. Malformed JSON is also an error — an activity envelope that
// cannot be parsed is not the same as one that explicitly declares
// unsupported.
func RunActivityProbe(goCtx context.Context, cmd string, selfOutputs map[string]any, nodeInputs map[string]any, session SessionVars) (ActivitySignal, error) {
	rendered, err := render(cmd, RenderContext{Self: selfOutputs, Inputs: nodeInputs, Session: session})
	if err != nil {
		return ActivitySignal{}, err
	}
	stdout, stderr, err := execHostScript(goCtx, rendered, session.WorkspaceDirPath)
	if err != nil {
		if len(stderr) > 0 {
			return ActivitySignal{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
		}
		return ActivitySignal{}, err
	}
	var wire activitySignalWire
	if err := json.Unmarshal(stdout, &wire); err != nil {
		return ActivitySignal{}, fmt.Errorf("activity probe: parse stdout as JSON: %w", err)
	}
	sig := ActivitySignal{
		Supported:        boolOrDefaultTrue(wire.Supported),
		ActivityExpected: boolOrDefaultTrue(wire.ActivityExpected),
		Fingerprint:      wire.Fingerprint,
	}
	if wire.ObservedAt != "" {
		observed, err := time.Parse(time.RFC3339, wire.ObservedAt)
		if err != nil {
			return ActivitySignal{}, fmt.Errorf("activity probe: parse observed_at %q: %w", wire.ObservedAt, err)
		}
		sig.ObservedAt = observed
	}
	return sig, nil
}

func boolOrDefaultTrue(v *bool) bool {
	return v == nil || *v
}
