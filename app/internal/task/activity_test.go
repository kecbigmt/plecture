package task

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunActivityProbe_Envelope(t *testing.T) {
	tests := []struct {
		name             string
		stdout           string
		wantStatus       ActivityStatus
		wantFingerprint  string
		wantObservedAt   string
		wantErrSubstring string
	}{
		{
			name:            "full envelope",
			stdout:          `{"status":"active","fingerprint":"fp-1","observed_at":"2026-08-18T00:04:06Z"}`,
			wantStatus:      ActivityActive,
			wantFingerprint: "fp-1",
			wantObservedAt:  "2026-08-18T00:04:06Z",
		},
		{
			name:       "none is the no-basis declaration",
			stdout:     `{"status":"none"}`,
			wantStatus: ActivityNone,
		},
		{
			name:            "idle",
			stdout:          `{"status":"idle","fingerprint":"fp-1"}`,
			wantStatus:      ActivityIdle,
			wantFingerprint: "fp-1",
		},
		{
			// Neither candidate default is safe: idle would pardon every
			// silence and hide real stalls, active would accuse probes that
			// never opted in.
			name:             "absent status is a parse error naming the value set",
			stdout:           `{"fingerprint":"fp-1"}`,
			wantErrSubstring: "none, idle, active",
		},
		{
			name:             "unknown status is a parse error naming the offending value",
			stdout:           `{"status":"opaque","fingerprint":"fp-1"}`,
			wantErrSubstring: `unknown status "opaque"`,
		},
		{
			name:             "malformed JSON is an error",
			stdout:           `not json`,
			wantErrSubstring: "parse stdout as JSON",
		},
		{
			name:             "unparseable observed_at is an error",
			stdout:           `{"status":"active","observed_at":"yesterday"}`,
			wantErrSubstring: "parse observed_at",
		},
	}
	session := SessionVars{Name: "owner/repo-1", WorkspaceDirPath: t.TempDir()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RunActivityProbe(context.Background(), "cat <<'EOF'\n"+tt.stdout+"\nEOF", nil, nil, session)
			if tt.wantErrSubstring != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunActivityProbe: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Fingerprint != tt.wantFingerprint {
				t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, tt.wantFingerprint)
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

func TestRunActivityProbe_NonZeroExitCarriesStderr(t *testing.T) {
	session := SessionVars{Name: "owner/repo-1", WorkspaceDirPath: t.TempDir()}
	_, err := RunActivityProbe(context.Background(), "echo 'pane is gone' >&2; exit 3", nil, nil, session)
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "pane is gone") {
		t.Errorf("err = %q, want it to carry stderr", err.Error())
	}
}
