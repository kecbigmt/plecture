package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ActivitySignal is the opaque, provider-neutral fact a declared activity
// probe reports about one task instance. Core never interprets what the probe
// actually observed (a terminal pane, an agent's turn boundary, a VCS
// workspace, ...) — it only compares Fingerprint and ObservedAt across
// evaluations and reads SilenceExpected as a pardon for this instance.
type ActivitySignal struct {
	Fingerprint string
	// SilenceExpected says this fingerprint's stability is intended, so the
	// silence must not be counted against the declaring instance. Only the
	// pardon direction is open to a probe: a wrong pardon hides a real
	// stall, while a wrongly withheld pardon is safe, because core's own
	// done_when-derived expectation still has to agree before anything is
	// called stalled. The accusation is always core's.
	SilenceExpected bool
	// ObservedAt is when the probe captured this fact. A zero value means
	// the probe did not report a timestamp.
	ObservedAt time.Time
}

// ActivityProbeExecError reports an activity probe whose command failed to run
// to completion. The exit code and stderr travel structured rather than folded
// into the message so a health report can name a persistently broken probe as
// a fault instead of reading it as silence.
type ActivityProbeExecError struct {
	// ExitCode is the command's exit status, or -1 when it never produced
	// one (the shell could not start, or the context was cancelled).
	ExitCode int
	Stderr   string
}

func (e *ActivityProbeExecError) Error() string {
	return fmt.Sprintf("activity probe: exited %d", e.ExitCode)
}

// activitySignalWire is the JSON envelope an activity probe writes to stdout.
type activitySignalWire struct {
	Fingerprint     string `json:"fingerprint"`
	SilenceExpected bool   `json:"silence_expected"`
	ObservedAt      string `json:"observed_at"`
}

// RunActivityProbe runs one activity probe and parses its stdout as an
// ActivitySignal.
//
// Stdout decides contribution and the exit code decides the health of the
// probe itself, so exiting 0 with empty stdout is how a probe says it has no
// evidence this time: a nil signal and no error, indistinguishable from an
// undeclared probe. A non-zero exit is an *ActivityProbeExecError, while a
// resolution failure, unparseable stdout, or an envelope missing the required
// fingerprint is a plain error. None of the three contributes evidence; the
// distinction only decides what the health report says about the probe.
func RunActivityProbe(goCtx context.Context, p Probe, session SessionVars) (*ActivitySignal, error) {
	ctx := p.context(session)
	resolved, err := resolveEffect(p.Action, healthRoots(ctx), ctx, p.From, nil)
	if err != nil {
		return nil, err
	}
	defer resolved.close()
	stdout, stderr, err := resolved.run(goCtx, session.WorkspaceDirPath, p.Env...)
	if err != nil {
		return nil, &ActivityProbeExecError{ExitCode: probeExitCode(err), Stderr: strings.TrimSpace(string(stderr))}
	}
	if strings.TrimSpace(string(stdout)) == "" {
		return nil, nil
	}
	var wire activitySignalWire
	if err := json.Unmarshal(stdout, &wire); err != nil {
		return nil, fmt.Errorf("activity probe: parse stdout as JSON: %w", err)
	}
	// Neither reading of a fingerprint-less envelope is safe: discarding it
	// silently and fabricating a fingerprint for it both let a broken probe
	// pass for a quiet one.
	if wire.Fingerprint == "" {
		return nil, fmt.Errorf("activity probe: envelope has no %q field", "fingerprint")
	}
	sig := &ActivitySignal{Fingerprint: wire.Fingerprint, SilenceExpected: wire.SilenceExpected}
	if wire.ObservedAt != "" {
		observed, err := time.Parse(time.RFC3339, wire.ObservedAt)
		if err != nil {
			return nil, fmt.Errorf("activity probe: parse observed_at %q: %w", wire.ObservedAt, err)
		}
		sig.ObservedAt = observed
	}
	return sig, nil
}

func probeExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
