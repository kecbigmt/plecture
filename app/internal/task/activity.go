package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ActivityStatus classifies how an activity probe interpreted its own
// observation attempt. It is a closed set rather than a pair of booleans:
// two booleans admit a combination that means nothing (no basis, yet
// activity expected) and hide one of the real states behind a field's
// default, so a probe with no opinion would have to pick a default that
// either suppresses stall detection or fabricates it.
//
// Only Idle defeats the expectation core derives from done_when. Nothing
// here can manufacture an expectation core does not already see.
type ActivityStatus string

const (
	// ActivityNone means the attempt found nothing to observe — the probe
	// ran, and there was no basis to judge activity. The observation is
	// discarded, evaluating the same as if no probe were declared.
	ActivityNone ActivityStatus = "none"
	// ActivityOpaque means the observation stands and is not interpretable
	// further: the fingerprint counts for freshness, and the probe makes no
	// claim about whether activity is due. A generic surface fingerprint (a
	// terminal pane's contents, a directory's shape) can honestly report
	// nothing more.
	ActivityOpaque ActivityStatus = "opaque"
	// ActivityIdle means the observation stands and quiet is normal right
	// now — the declaring instance's own expectation is narrowed.
	ActivityIdle ActivityStatus = "idle"
	// ActivityActive means the observation stands and activity is due, so a
	// frozen fingerprint is stall evidence.
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
// Status carries no default: the whole point of the enum is that every state
// is stated, so an omitted or unrecognized value is a parse error rather than
// a silent fallback to whichever state happens to be least disruptive.
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
	case ActivityNone, ActivityOpaque, ActivityIdle, ActivityActive:
		return ActivityStatus(raw), nil
	case "":
		return "", fmt.Errorf("activity probe: envelope has no %q field (want one of %s)", "status", activityStatusList)
	default:
		return "", fmt.Errorf("activity probe: unknown status %q (want one of %s)", raw, activityStatusList)
	}
}

const activityStatusList = "none, opaque, idle, active"
