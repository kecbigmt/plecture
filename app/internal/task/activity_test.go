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
		wantSupported    bool
		wantExpected     bool
		wantFingerprint  string
		wantObservedAt   string
		wantErrSubstring string
	}{
		{
			name:            "full envelope",
			stdout:          `{"supported":true,"activity_expected":true,"fingerprint":"fp-1","observed_at":"2026-08-18T00:04:06Z"}`,
			wantSupported:   true,
			wantExpected:    true,
			wantFingerprint: "fp-1",
			wantObservedAt:  "2026-08-18T00:04:06Z",
		},
		{
			// A probe reporting facts without bothering to declare support is
			// implicitly declaring it.
			name:            "omitted supported defaults to true",
			stdout:          `{"fingerprint":"fp-1"}`,
			wantSupported:   true,
			wantExpected:    true,
			wantFingerprint: "fp-1",
		},
		{
			// The load-bearing default: a generic probe with no opinion must
			// not silently narrow the expectation core derived from done_when,
			// or it would suppress the stall detection it exists to feed.
			name:            "omitted activity_expected defaults to true",
			stdout:          `{"supported":true,"fingerprint":"fp-1"}`,
			wantSupported:   true,
			wantExpected:    true,
			wantFingerprint: "fp-1",
		},
		{
			name:         "explicit activity_expected false narrows",
			stdout:       `{"supported":true,"activity_expected":false,"fingerprint":"fp-1"}`,
			wantExpected: false, wantSupported: true, wantFingerprint: "fp-1",
		},
		{
			name:          "explicit supported false is the no-basis declaration",
			stdout:        `{"supported":false}`,
			wantSupported: false,
			wantExpected:  true,
		},
		{
			name:             "malformed JSON is an error",
			stdout:           `not json`,
			wantErrSubstring: "parse stdout as JSON",
		},
		{
			name:             "unparseable observed_at is an error",
			stdout:           `{"supported":true,"observed_at":"yesterday"}`,
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
			if got.Supported != tt.wantSupported {
				t.Errorf("Supported = %v, want %v", got.Supported, tt.wantSupported)
			}
			if got.ActivityExpected != tt.wantExpected {
				t.Errorf("ActivityExpected = %v, want %v", got.ActivityExpected, tt.wantExpected)
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
