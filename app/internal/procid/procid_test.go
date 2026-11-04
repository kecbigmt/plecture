package procid

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestCapture_RunningProcessIdentifiesItself(t *testing.T) {
	id, err := Capture(os.Getpid())
	if err != nil {
		t.Fatalf("Capture(self): %v", err)
	}
	if id.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", id.PID, os.Getpid())
	}
	if id.Start <= 0 {
		t.Errorf("Start = %d, want a positive numeric start time", id.Start)
	}
}

func TestCapture_IsStableAcrossReads(t *testing.T) {
	first, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two captures of one process differ: %+v vs %+v", first, second)
	}
}

func TestCapture_DistinguishesProcessesStartedApart(t *testing.T) {
	cmd := startSleeper(t)
	child, err := Capture(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	self, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if child.PID == self.PID {
		t.Fatalf("test setup: both captures name pid %d", child.PID)
	}
}

func TestCapture_ExitedProcessIsNotFound(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	_, err := Capture(cmd.Process.Pid)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Capture(exited pid) error = %v, want ErrNotRunning", err)
	}
}

func TestCapture_RejectsNonPositivePid(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := Capture(pid); err == nil {
			t.Errorf("Capture(%d) succeeded, want an error", pid)
		}
	}
}

func TestVerify_Outcomes(t *testing.T) {
	self, err := Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// pid 1 started long before this test binary, so its start time cannot
	// fall inside the rounding tolerance of the test's own.
	other, err := Capture(1)
	if err != nil {
		t.Skipf("cannot read pid 1: %v", err)
	}
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		recorded contract.ProcessIdentity
		wantErr  error
	}{
		{"same process passes", self, nil},
		{"rounding within the tolerance passes", shift(self, startTolerance), nil},
		{"one past the tolerance is a different process", shift(self, startTolerance+1), ErrReused},
		{"another process's start time under this pid is a reuse", contract.ProcessIdentity{PID: self.PID, Start: other.Start, Boot: other.Boot}, ErrReused},
		{"a different boot is a reuse", withBoot(self, "another-boot"), ErrReused},
		{"dead pid is not running", contract.ProcessIdentity{PID: exited.Process.Pid, Start: self.Start, Boot: self.Boot}, ErrNotRunning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(tc.recorded)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerify_PidExistenceAloneForARecordWithoutAStartTime(t *testing.T) {
	if err := VerifyPIDOnly(os.Getpid()); err != nil {
		t.Fatalf("VerifyPIDOnly(self): %v", err)
	}
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPIDOnly(exited.Process.Pid); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("VerifyPIDOnly(dead) = %v, want ErrNotRunning", err)
	}
}

func shift(id contract.ProcessIdentity, by int64) contract.ProcessIdentity {
	id.Start += by
	return id
}

func withBoot(id contract.ProcessIdentity, boot string) contract.ProcessIdentity {
	id.Boot = boot
	return id
}

func TestEncodeDecode_RoundTripsAnIdentity(t *testing.T) {
	want := contract.ProcessIdentity{PID: 42, Start: 7001, Boot: "boot-a"}
	got, err := Decode(Encode(want))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Fatalf("Decode(Encode(x)) = %+v, want %+v", got, want)
	}
}

func TestDecode_RejectsWhatIsNotAnIdentity(t *testing.T) {
	for _, token := range []string{"", "not json", `{"pid":0}`, `{"pid":-3,"start":1}`, `[]`} {
		if _, err := Decode(token); err == nil {
			t.Errorf("Decode(%q) succeeded, want an error", token)
		}
	}
}
