package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MovementSignal is the opaque, provider-neutral fact a declared
// movement-signal command reports about one task instance. Core never
// interprets what the command actually observed (a terminal pane, an agent
// transcript, a VCS workdir, ...) — it only compares Fingerprint and
// ObservedAt across evaluations and reads Supported/MovementExpected as
// plain booleans.
type MovementSignal struct {
	// Supported reports whether this instance currently has a basis to
	// judge movement at all. A command may run successfully and still
	// report false — an explicit "nothing to say right now" declaration,
	// distinct from no command being declared.
	Supported bool
	// MovementExpected is the signal source's own contribution to whether
	// movement is currently expected (e.g. "there is an active turn").
	// Core combines this with its own done_when-derived expectation; a
	// signal can narrow that expectation but never manufacture one core's
	// done_when/task-state evaluation does not already see.
	MovementExpected bool
	// Fingerprint is an opaque token that changes whenever the source
	// observes new movement. Core never parses it, only compares it.
	Fingerprint string
	// ObservedAt is when the source captured this fact. A zero value means
	// the source did not report a timestamp.
	ObservedAt time.Time
}

// movementSignalWire is the JSON shape a movement-signal command writes to
// stdout. SupportedField is a pointer so an omitted field defaults to true
// (a command that reports facts without bothering to declare "supported"
// is implicitly declaring support), while an explicit `"supported": false`
// is the no-signal declaration.
type movementSignalWire struct {
	Supported        *bool  `json:"supported"`
	MovementExpected bool   `json:"movement_expected"`
	Fingerprint      string `json:"fingerprint"`
	ObservedAt       string `json:"observed_at"`
}

// RunMovementSignal renders cmd against the task's own outputs, its resolved
// node inputs, and session vars (mirroring RunHealthcheck), runs it via
// bash -c, and parses its stdout as a MovementSignal fact. A non-zero exit
// or a render failure is returned as an error carrying stderr, mirroring
// RunHealthcheck. Malformed JSON is also an error — a movement signal that
// cannot be parsed is not the same as one that explicitly declares
// unsupported.
func RunMovementSignal(goCtx context.Context, cmd string, selfOutputs map[string]any, nodeInputs map[string]any, session SessionVars) (MovementSignal, error) {
	rendered, err := render(cmd, RenderContext{Self: selfOutputs, Inputs: nodeInputs, Session: session})
	if err != nil {
		return MovementSignal{}, err
	}
	stdout, stderr, err := execHostScript(goCtx, rendered, session.WorkdirPath)
	if err != nil {
		if len(stderr) > 0 {
			return MovementSignal{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr)))
		}
		return MovementSignal{}, err
	}
	var wire movementSignalWire
	if err := json.Unmarshal(stdout, &wire); err != nil {
		return MovementSignal{}, fmt.Errorf("movement signal: parse stdout as JSON: %w", err)
	}
	supported := true
	if wire.Supported != nil {
		supported = *wire.Supported
	}
	sig := MovementSignal{
		Supported:        supported,
		MovementExpected: wire.MovementExpected,
		Fingerprint:      wire.Fingerprint,
	}
	if wire.ObservedAt != "" {
		observed, err := time.Parse(time.RFC3339, wire.ObservedAt)
		if err != nil {
			return MovementSignal{}, fmt.Errorf("movement signal: parse observed_at %q: %w", wire.ObservedAt, err)
		}
		sig.ObservedAt = observed
	}
	return sig, nil
}
