package pluginservice

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// writeScript writes a #!/usr/bin/env bash script and returns its absolute
// path. Tests use real short-lived subprocesses rather than fakes because
// this package's whole job is process lifecycle (start, crash, signal-
// terminate), which a fake process can't exercise.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/usr/bin/env bash\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestSupervisor builds a Supervisor with millisecond-scale tunables so
// tests don't wait on production-scale poll/backoff intervals.
func newTestSupervisor(source PluginSource) *Supervisor {
	sup := NewSupervisor(source)
	sup.poll = 10 * time.Millisecond
	sup.baseBackoff = 10 * time.Millisecond
	sup.maxBackoff = 40 * time.Millisecond
	sup.waitDelay = 2 * time.Second
	return sup
}

// mountWithService builds a Mounted declaring exactly one service backed by
// scriptPath, so BuildDeclarations resolves ExecPath to scriptPath directly
// (Dir is "", and filepath.Join("", scriptPath) == scriptPath).
func mountWithService(pluginID, serviceName, scriptPath string, restart string, requiredEnv []string) plugins.Mounted {
	return plugins.Mounted{
		ID: pluginID,
		Manifest: plugins.Manifest{
			Executables: []plugins.Executable{{Name: serviceName, Path: scriptPath}},
			Services: []plugins.Service{{
				Name:        serviceName,
				Executable:  serviceName,
				Restart:     restart,
				RequiredEnv: requiredEnv,
				Health:      plugins.ServiceHealth{Type: plugins.ServiceHealthProcess},
			}},
		},
	}
}

func staticSource(mounted ...plugins.Mounted) PluginSource {
	return func() ([]plugins.Mounted, *plugins.Lockfile, error) {
		return mounted, &plugins.Lockfile{}, nil
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// alive reports whether pid still exists, by sending the zero signal —
// delivers nothing, just probes for ESRCH.
func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func TestSupervisor_StartsDeclaredServiceAndStopsOnShutdown(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "long-lived", "trap 'exit 0' TERM\nwhile true; do sleep 0.01; done\n")
	mounted := mountWithService("p", "svc", script, plugins.ServiceRestartOnFailure, nil)

	sup := newTestSupervisor(staticSource(mounted))
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		sup.Run(ctx)
	}()

	waitFor(t, time.Second, func() bool {
		st, ok := sup.Status.Get("p/svc")
		return ok && st.Running && st.PID > 0
	})
	st, _ := sup.Status.Get("p/svc")
	if st.Health != domain.HealthHealthy {
		t.Fatalf("Health = %v, want healthy while running", st.Health)
	}
	if !alive(st.PID) {
		t.Fatalf("process %d not alive while service reports running", st.PID)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	if alive(st.PID) {
		t.Fatalf("process %d still alive after Run returned", st.PID)
	}
	stAfter, ok := sup.Status.Get("p/svc")
	if !ok || stAfter.Running {
		t.Fatalf("Status after shutdown = %+v, want Running = false", stAfter)
	}
}

func TestSupervisor_RestartsCrashedServiceWithBackoff(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "crasher", "exit 7\n")
	mounted := mountWithService("p", "crasher", script, plugins.ServiceRestartOnFailure, nil)

	sup := newTestSupervisor(staticSource(mounted))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		st, ok := sup.Status.Get("p/crasher")
		return ok && st.RestartCount >= 3
	})
	st, _ := sup.Status.Get("p/crasher")
	if st.Running {
		t.Fatalf("Status = %+v, want Running = false between restarts", st)
	}
	if st.LastError == "" {
		t.Fatal("LastError = \"\", want the crash's exit error recorded")
	}
}

func TestSupervisor_RestartNeverDoesNotRestart(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "once", "exit 1\n")
	mounted := mountWithService("p", "once", script, plugins.ServiceRestartNever, nil)

	sup := newTestSupervisor(staticSource(mounted))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, time.Second, func() bool {
		st, ok := sup.Status.Get("p/once")
		return ok && !st.Running && st.Health == domain.HealthUndeclared
	})
	// Give it several extra poll ticks to (wrongly) restart if the policy
	// were not honored.
	time.Sleep(80 * time.Millisecond)
	st, _ := sup.Status.Get("p/once")
	if st.RestartCount != 0 {
		t.Fatalf("RestartCount = %d, want 0 for restart = never", st.RestartCount)
	}
}

func TestSupervisor_MissingRequiredEnvStaysInert(t *testing.T) {
	dir := t.TempDir()
	// Would fail loudly if ever started — proves the supervisor never
	// attempts to run it.
	script := writeScript(t, dir, "should-not-run", "exit 1\n")
	os.Unsetenv("PLECT_TEST_MISSING_TOKEN_XYZ") // ensure genuinely unset
	mounted := mountWithService("p", "inert", script, plugins.ServiceRestartOnFailure, []string{"PLECT_TEST_MISSING_TOKEN_XYZ"})

	sup := newTestSupervisor(staticSource(mounted))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, time.Second, func() bool {
		st, ok := sup.Status.Get("p/inert")
		return ok && st.LastError != ""
	})
	time.Sleep(50 * time.Millisecond) // let a few poll ticks pass
	st, _ := sup.Status.Get("p/inert")
	if st.Running || st.RestartCount != 0 {
		t.Fatalf("Status = %+v, want an inert service that never started", st)
	}
}

func TestSupervisor_CrashingServiceDoesNotStopHealthyService(t *testing.T) {
	dir := t.TempDir()
	healthy := writeScript(t, dir, "healthy", "trap 'exit 0' TERM\nwhile true; do sleep 0.01; done\n")
	crasher := writeScript(t, dir, "crasher", "exit 1\n")
	mounted := []plugins.Mounted{
		mountWithService("p", "healthy", healthy, plugins.ServiceRestartOnFailure, nil),
		mountWithService("p2", "crasher", crasher, plugins.ServiceRestartOnFailure, nil),
	}

	sup := newTestSupervisor(staticSource(mounted...))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	// Let the crasher restart a few times while asserting the healthy
	// service never drops out of "running" — proving the crash loop is
	// isolated to its own goroutine.
	waitFor(t, 2*time.Second, func() bool {
		st, ok := sup.Status.Get("p2/crasher")
		return ok && st.RestartCount >= 2
	})
	hst, ok := sup.Status.Get("p/healthy")
	if !ok || !hst.Running {
		t.Fatalf("healthy service Status = %+v, want it to stay running throughout the sibling's crash loop", hst)
	}
}

func TestSupervisor_RestartsServiceWhenPluginContentChanges(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "svc", "trap 'exit 0' TERM\nwhile true; do sleep 0.01; done\n")

	var hash atomic.Value
	hash.Store("sha256:v1")
	source := func() ([]plugins.Mounted, *plugins.Lockfile, error) {
		mounted := mountWithService("p", "svc", script, plugins.ServiceRestartOnFailure, nil)
		lock := &plugins.Lockfile{Plugins: []plugins.PluginLockEntry{{ID: "p", ContentHash: hash.Load().(string)}}}
		return []plugins.Mounted{mounted}, lock, nil
	}

	sup := newTestSupervisor(source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, time.Second, func() bool {
		st, ok := sup.Status.Get("p/svc")
		return ok && st.Running
	})
	firstPID, _ := sup.Status.Get("p/svc")

	hash.Store("sha256:v2") // simulate `plect plugin update` repointing the lock entry

	waitFor(t, time.Second, func() bool {
		st, ok := sup.Status.Get("p/svc")
		return ok && st.Running && st.PID != firstPID.PID && st.ContentHash == "sha256:v2"
	})
}
