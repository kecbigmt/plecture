package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ProgressSignal is the opaque, provider-neutral fact a declared
// progress-signal command reports about one task instance. Core never
// interprets what the command actually observed (a terminal pane, an agent
// transcript, a VCS worktree, ...) — it only compares Fingerprint and
// ObservedAt across evaluations and reads Supported/ProgressExpected as
// plain booleans.
type ProgressSignal struct {
	// Supported reports whether this instance currently has a basis to
	// judge progress at all. A command may run successfully and still
	// report false — an explicit "nothing to say right now" declaration,
	// distinct from no command being declared.
	Supported bool
	// ProgressExpected is the signal source's own contribution to whether
	// progress is currently expected (e.g. "there is an active turn").
	// Core combines this with its own done_when-derived expectation; a
	// signal can narrow that expectation but never manufacture one core's
	// done_when/task-state evaluation does not already see.
	ProgressExpected bool
	// Fingerprint is an opaque token that changes whenever the source
	// observes new progress. Core never parses it, only compares it.
	Fingerprint string
	// ObservedAt is when the source captured this fact. A zero value means
	// the source did not report a timestamp.
	ObservedAt time.Time
}

// progressSignalWire is the JSON shape a progress-signal command writes to
// stdout. SupportedField is a pointer so an omitted field defaults to true
// (a command that reports facts without bothering to declare "supported"
// is implicitly declaring support), while an explicit `"supported": false`
// is the no-signal declaration.
type progressSignalWire struct {
	Supported        *bool  `json:"supported"`
	ProgressExpected bool   `json:"progress_expected"`
	Fingerprint      string `json:"fingerprint"`
	ObservedAt       string `json:"observed_at"`
}

// RunProgressSignal renders cmd against the task's own outputs, its resolved
// node inputs, and session vars (mirroring RunHealthcheck), runs it via
// bash -c, and parses its stdout as a ProgressSignal fact. A non-zero exit
// or a render failure is returned as an error carrying stderr, mirroring
// RunHealthcheck. Malformed JSON is also an error — a progress signal that
// cannot be parsed is not the same as one that explicitly declares
// unsupported.
func RunProgressSignal(goCtx context.Context, cmd string, selfOutputs map[string]any, nodeInputs map[string]any, session SessionVars) (ProgressSignal, error) {
	rendered, err := render(cmd, RenderContext{Self: selfOutputs, Inputs: nodeInputs, Session: session})
	if err != nil {
		return ProgressSignal{}, err
	}
	stdout, stderr, err := execHostScript(goCtx, rendered, session.WorktreePath)
	if err != nil {
		if len(stderr) > 0 {
			return ProgressSignal{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
		}
		return ProgressSignal{}, err
	}
	var wire progressSignalWire
	if err := json.Unmarshal(stdout, &wire); err != nil {
		return ProgressSignal{}, fmt.Errorf("progress signal: parse stdout as JSON: %w", err)
	}
	supported := true
	if wire.Supported != nil {
		supported = *wire.Supported
	}
	sig := ProgressSignal{
		Supported:        supported,
		ProgressExpected: wire.ProgressExpected,
		Fingerprint:      wire.Fingerprint,
	}
	if wire.ObservedAt != "" {
		observed, err := time.Parse(time.RFC3339, wire.ObservedAt)
		if err != nil {
			return ProgressSignal{}, fmt.Errorf("progress signal: parse observed_at %q: %w", wire.ObservedAt, err)
		}
		sig.ObservedAt = observed
	}
	return sig, nil
}
