package ghapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var errTest = errors.New("exit status 1")

type fakeRunner struct {
	gotDir  string
	gotName string
	gotArgs []string
	stdout  []byte
	stderr  []byte
	err     error
}

func (f *fakeRunner) Run(ctx context.Context, dir string, mirror bool, name string, args ...string) ([]byte, []byte, error) {
	f.gotDir = dir
	f.gotName = name
	f.gotArgs = args
	return f.stdout, f.stderr, f.err
}

func TestDirect_RunsGhAPI(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"ok":true}`)}
	c := Direct()
	c.Runner = runner

	out, err := c.JSON(context.Background(), "repos/acme/widgets/issues/1")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(out) != `{"ok":true}` {
		t.Errorf("out = %q", out)
	}
	if runner.gotName != "gh" {
		t.Errorf("program = %q, want gh", runner.gotName)
	}
	want := []string{"api", "repos/acme/widgets/issues/1"}
	if strings.Join(runner.gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", runner.gotArgs, want)
	}
}

func TestViaWatcher_RunsGhAPISubcommand(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{}`)}
	c := ViaWatcher("/opt/plugins/github/bin/github-watcher")
	c.Runner = runner

	if _, err := c.JSON(context.Background(), "repos/acme/widgets/pulls/2"); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if runner.gotName != "/opt/plugins/github/bin/github-watcher" {
		t.Errorf("program = %q", runner.gotName)
	}
	want := []string{"gh-api", "repos/acme/widgets/pulls/2"}
	if strings.Join(runner.gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", runner.gotArgs, want)
	}
}

func TestJSON_FailurePropagatesStderr(t *testing.T) {
	runner := &fakeRunner{stderr: []byte("HTTP 404: Not Found"), err: errTest}
	c := Direct()
	c.Runner = runner

	_, err := c.JSON(context.Background(), "repos/acme/widgets/issues/999")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v, want it to contain gh's stderr", err)
	}
}

func TestJSON_FailureWithoutStderrWrapsUnderlyingError(t *testing.T) {
	runner := &fakeRunner{err: errTest}
	c := Direct()
	c.Runner = runner

	_, err := c.JSON(context.Background(), "repos/acme/widgets/issues/999")
	if err == nil || !strings.Contains(err.Error(), errTest.Error()) {
		t.Fatalf("error = %v, want it to wrap %v", err, errTest)
	}
}
