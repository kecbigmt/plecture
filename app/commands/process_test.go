package commands

import (
	"errors"
	"os"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/procid"
)

func TestVerifyProcessIdentity(t *testing.T) {
	self, err := procid.Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	reused := self
	reused.Start += 1000

	if err := verifyProcessIdentity(procid.Encode(self)); err != nil {
		t.Errorf("the recorded process: %v", err)
	}
	if err := verifyProcessIdentity(procid.Encode(reused)); !errors.Is(err, procid.ErrReused) {
		t.Errorf("a reused pid: error = %v, want ErrReused", err)
	}
	if err := verifyProcessIdentity("garbage"); err == nil {
		t.Error("a token that is not an identity was accepted")
	}
}

func TestProcessCommandIsHiddenFromHelp(t *testing.T) {
	if !processCmd.Hidden {
		t.Error("plect process is a plugin-facing verb and must not appear in plect --help")
	}
}
