package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ActivityStatus is what the absence of new activity would mean, as the
// probe reads its own surface. The fingerprint carries the fact of activity;
// this carries how silence should be read.
//
// Only Idle pardons silence, and only Idle requires the probe to have
// positively established anything. That asymmetry is deliberate: a wrong
// pardon hides a real stall, while a wrongly withheld pardon is safe,
// because core's own done_when-derived expectation still has to agree
// before anything is called stalled. The accusation is always core's; a
// probe holds only the pardon channel.
type ActivityStatus string

const (
	// ActivityNone means the attempt found nothing to observe — the probe
	// ran, and there was no basis to judge activity. The observation is
	// discarded, evaluating the same as if no probe were declared.
	ActivityNone ActivityStatus = "none"
	// ActivityIdle means the probe positively established that quiet is
	// normal right now (the turn is over, the queue is empty), so the
	// declaring instance's own expectation is narrowed.
	ActivityIdle ActivityStatus = "idle"
	// ActivityActive is the residual: something was observed, and idle was
	// not established, so activity cannot be ruled out. A generic surface
	// fingerprint reports this honestly — within what it can see, silence
	// is not excused.
	ActivityActive ActivityStatus = "active"
)

// ActivitySignal is the opaque, provider-neutral fact a declared activity
// probe reports about one task instance. Core never interprets what the probe
// actually observed (a terminal pane, an agent's turn boundary, a VCS
// workspace, ...) — it only compares Fingerprint and ObservedAt across
// evaluations and reads Status as a classification of the attempt.
type ActivitySignal struct {
	Status      ActivityStatus
	Fingerprint string
	// ObservedAt is when the probe captured this fact. A zero value means
	// the probe did not report a timestamp.
	ObservedAt time.Time
}

// activitySignalWire is the JSON envelope an activity probe writes to stdout.
// Status carries no default: an omitted or unrecognized value is a parse
// error rather than a silent fallback, because the two candidate defaults
// are a blanket pardon (hiding every stall) and a blanket accusation
// (stalling probes that never opted in).
type activitySignalWire struct {
	Status      string `json:"status"`
	Fingerprint string `json:"fingerprint"`
	ObservedAt  string `json:"observed_at"`
}

// RunActivityProbe renders cmd against the task's own outputs, its resolved
// node inputs, and session vars (mirroring RunAliveProbe), runs it via
// bash -c, and parses its stdout as an ActivitySignal. A non-zero exit
// or a render failure is returned as an error carrying stderr, mirroring
// RunAliveProbe. Malformed JSON is also an error — an activity envelope that
// cannot be parsed is not the same as one that declares itself none.
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
	status, err := parseActivityStatus(wire.Status)
	if err != nil {
		return ActivitySignal{}, err
	}
	sig := ActivitySignal{Status: status, Fingerprint: wire.Fingerprint}
	if wire.ObservedAt != "" {
		observed, err := time.Parse(time.RFC3339, wire.ObservedAt)
		if err != nil {
			return ActivitySignal{}, fmt.Errorf("activity probe: parse observed_at %q: %w", wire.ObservedAt, err)
		}
		sig.ObservedAt = observed
	}
	return sig, nil
}

func parseActivityStatus(raw string) (ActivityStatus, error) {
	switch ActivityStatus(raw) {
	case ActivityNone, ActivityIdle, ActivityActive:
		return ActivityStatus(raw), nil
	case "":
		return "", fmt.Errorf("activity probe: envelope has no %q field (want one of %s)", "status", activityStatusList)
	default:
		return "", fmt.Errorf("activity probe: unknown status %q (want one of %s)", raw, activityStatusList)
	}
}

const activityStatusList = "none, idle, active"
