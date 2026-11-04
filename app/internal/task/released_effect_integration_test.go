//go:build integration

package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A session keeps the declarations and recorded outputs it was created with
// while the executables are whatever is installed when an action runs, so a
// plugin update must not break it. Each fixture under testdata/compat/<tag>/
// runs under the released declarations and any current declaration still
// present, against the plugin's checked-in scripts. A retired declaration
// still runs from its released fixture. No invocation record
// is kept because the pass is the contract.
func TestShippedEffects_ReleasedDeclarationsAndRecordsStillRun(t *testing.T) {
	for _, source := range repoPluginDirs(t) {
		fixtures, err := filepath.Glob(filepath.Join(source, "testdata", "compat", "*", "scenarios.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, fixture := range fixtures {
			dir := filepath.Dir(fixture)
			name := filepath.Base(source) + "/" + filepath.Base(dir)
			declarations := []struct{ name, dir string }{
				{"released-declarations", filepath.Join(dir, "config")},
				{"current-declarations", ""},
			}
			for _, d := range declarations {
				t.Run(name+"/"+d.name, func(t *testing.T) {
					scenarios := loadScenarioFile(t, fixture)
					if len(scenarios) == 0 {
						t.Fatalf("%s declares no scenarios", fixture)
					}
					produced := recordEffectsUnder(t, source, d.dir, true, scenarios, d.dir == "")
					for _, declined := range declinedSections(produced) {
						t.Errorf("%s", declined)
					}
				})
			}
		}
	}
}

func declinedSections(produced string) []string {
	var out []string
	var header string
	for _, line := range strings.Split(produced, "\n") {
		switch {
		case strings.HasPrefix(line, "== "):
			header = strings.TrimPrefix(line, "== ")
		case strings.HasPrefix(line, "declined: "):
			out = append(out, header+": "+line)
		}
	}
	return out
}

func TestKillLaunchedWorkers_OnlySignalsWorkersOfThisSandbox(t *testing.T) {
	stateDir := t.TempDir()
	start := func(marker string) *exec.Cmd {
		cmd := exec.Command("sh", "-c", "sleep 600; :", marker)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _, _ = cmd.Process.Wait() })
		return cmd
	}
	owned := start(filepath.Join(stateDir, "queue-worker"))
	foreign := start("another-session-worker")
	for name, cmd := range map[string]*exec.Cmd{"owned": owned, "foreign": foreign} {
		dir := filepath.Join(stateDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		pid := []byte(strconv.Itoa(cmd.Process.Pid))
		if err := os.WriteFile(filepath.Join(dir, "worker.pid"), pid, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	killLaunchedWorkers(stateDir)

	exited := func(cmd *exec.Cmd, within time.Duration) bool {
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(within):
			return false
		}
	}
	if !exited(owned, 5*time.Second) {
		t.Error("the sandbox's own worker should have been terminated")
	}
	if exited(foreign, time.Second) {
		t.Error("a worker outside this sandbox must be left running")
	}
}
