package task

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunActivityProbe_Envelope(t *testing.T) {
	tests := []struct {
		name                string
		stdout              string
		wantNoSignal        bool
		wantFingerprint     string
		wantSilenceExpected bool
		wantObservedAt      string
		wantErrSubstring    string
	}{
		{
			name:            "full envelope",
			stdout:          `{"fingerprint":"fp-1","observed_at":"2026-08-18T00:04:06Z"}`,
			wantFingerprint: "fp-1",
			wantObservedAt:  "2026-08-18T00:04:06Z",
		},
		{
			name:                "silence_expected is the pardon",
			stdout:              `{"fingerprint":"fp-1","silence_expected":true}`,
			wantFingerprint:     "fp-1",
			wantSilenceExpected: true,
		},
		{
			name:            "silence_expected false reads as no pardon",
			stdout:          `{"fingerprint":"fp-1","silence_expected":false}`,
			wantFingerprint: "fp-1",
		},
		{
			name:         "empty stdout is the no-basis declaration",
			stdout:       "",
			wantNoSignal: true,
		},
		{
			name:         "blank stdout is the no-basis declaration",
			stdout:       "   \n",
			wantNoSignal: true,
		},
		{
			// The two candidate readings of a fingerprint-less envelope are
			// a silent discard and a fabricated fingerprint; both hide a
			// broken probe, so it is rejected instead.
			name:             "envelope without a fingerprint is invalid output",
			stdout:           `{"silence_expected":true}`,
			wantErrSubstring: `no "fingerprint" field`,
		},
		{
			name:             "the retired status enum is invalid output",
			stdout:           `{"status":"active"}`,
			wantErrSubstring: `no "fingerprint" field`,
		},
		{
			name:             "malformed JSON is an error",
			stdout:           `not json`,
			wantErrSubstring: "parse stdout as JSON",
		},
		{
			name:             "unparseable observed_at is an error",
			stdout:           `{"fingerprint":"fp-1","observed_at":"yesterday"}`,
			wantErrSubstring: "parse observed_at",
		},
	}
	session := SessionVars{Name: "owner/repo-1", WorkspaceDirPath: t.TempDir()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RunActivityProbe(context.Background(), "cat <<'EOF'\n"+tt.stdout+"\nEOF", nil, nil, session, "")
			if tt.wantErrSubstring != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunActivityProbe: %v", err)
			}
			if tt.wantNoSignal {
				if got != nil {
					t.Fatalf("signal = %+v, want nil (no basis)", got)
				}
				return
			}
			if got == nil {
				t.Fatal("signal = nil, want an envelope")
			}
			if got.Fingerprint != tt.wantFingerprint {
				t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, tt.wantFingerprint)
			}
			if got.SilenceExpected != tt.wantSilenceExpected {
				t.Errorf("SilenceExpected = %v, want %v", got.SilenceExpected, tt.wantSilenceExpected)
			}
			wantObserved := time.Time{}
			if tt.wantObservedAt != "" {
				wantObserved, _ = time.Parse(time.RFC3339, tt.wantObservedAt)
			}
			if !got.ObservedAt.Equal(wantObserved) {
				t.Errorf("ObservedAt = %v, want %v", got.ObservedAt, wantObserved)
			}
		})
	}
}

func TestRunActivityProbe_NonZeroExitCarriesExitCodeAndStderr(t *testing.T) {
	session := SessionVars{Name: "owner/repo-1", WorkspaceDirPath: t.TempDir()}
	sig, err := RunActivityProbe(context.Background(), "echo 'pane is gone' >&2; exit 3", nil, nil, session, "")
	if sig != nil {
		t.Fatalf("signal = %+v, want nil", sig)
	}
	var execErr *ActivityProbeExecError
	if !errors.As(err, &execErr) {
		t.Fatalf("err = %v, want *ActivityProbeExecError", err)
	}
	if execErr.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", execErr.ExitCode)
	}
	if execErr.Stderr != "pane is gone" {
		t.Errorf("Stderr = %q, want %q", execErr.Stderr, "pane is gone")
	}
}
