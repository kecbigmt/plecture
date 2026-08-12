package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/task"
	contract "github.com/plecture/plect/contracts/state"
)

// repoRoot locates the repository root from this test file's own path, so a
// test that reads a shipped config file does not depend on the working
// directory it is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// loadShippedProvider loads a provider config that ships with a plugin,
// exercising the same loader the CLI uses at runtime.
func loadShippedProvider(t *testing.T, pluginDir, id string) config.ProviderConfig {
	t.Helper()
	cfg := &config.Config{PluginDirs: []string{filepath.Join(repoRoot(t), "plugins", pluginDir)}}
	provs, err := cfg.LoadProviders()
	if err != nil {
		t.Fatalf("load shipped providers: %v", err)
	}
	prov, ok := provs[id]
	if !ok {
		t.Fatalf("shipped provider %q not found", id)
	}
	return prov
}

// TestShippedProvider_ResolvesResourceIdentifiersOffline pins that the
// shipped provider's resolver derives session ids from resource identifiers
// with regex and template alone — the core never asks a network for a name.
func TestShippedProvider_ResolvesResourceIdentifiersOffline(t *testing.T) {
	prov := loadShippedProvider(t, "github-provider", "github")
	if !prov.HasResolver() {
		t.Fatal("the shipped provider must declare a resolver")
	}

	tests := []struct {
		name     string
		resource string
		want     string
		matched  bool
	}{
		{"issue url", "https://github.com/acme/widgets/issues/42", "acme/widgets-42", true},
		{"pull request url", "https://github.com/acme/widgets/pull/7", "acme/widgets-7", true},
		{"issue url with trailing path", "https://github.com/acme/widgets/issues/42#issuecomment-1", "acme/widgets-42", true},
		{"unrelated url", "https://example.test/acme/widgets/items/42", "", false},
		{"bare identifier", "acme/widgets-42", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched, err := tryResolveName(prov, tt.resource)
			if err != nil {
				t.Fatalf("tryResolveName: %v", err)
			}
			if matched != tt.matched {
				t.Fatalf("matched = %v, want %v", matched, tt.matched)
			}
			if got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestShippedProvider_SetupHookDoesNotShellInjectResourceID pins that a
// resource id carrying shell metacharacters is passed to the setup hook
// literally rather than being interpreted by the shell that renders it. The
// injected command runs (if unescaped) regardless of whether
// plect-github-provider is even on PATH, since bash executes every
// semicolon-separated command in "cmd1; cmd2" independent of cmd1's exit
// status — so this test needs no built binaries.
func TestShippedProvider_SetupHookDoesNotShellInjectResourceID(t *testing.T) {
	prov := loadShippedProvider(t, "github-provider", "github")
	marker := filepath.Join(t.TempDir(), "pwned")
	malicious := `https://github.com/acme/widgets/issues/1"; touch ` + marker + `; echo "`

	tasks := map[string]*contract.TaskState{}
	vars := task.WorkflowHookVars{ResourceID: malicious, SessionName: "acme/widgets-1"}
	// Setup is expected to fail (plect-github-provider is not on PATH in this
	// test and the resource id is not a valid URL); only the absence of the
	// injected side effect is under test.
	_, _ = task.RunWorkflowSetup(prov, vars, tasks, nil)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("resource id was shell-interpreted: injected command executed")
	}
}

// TestShippedProvider_CleanupHookDoesNotShellInjectWorkdirOrBranch mirrors the
// setup case for cleanup, whose template interpolates the persisted workdir
// and branch (sourced from setup's own outputs, which in turn derive from the
// session's tag — not fully outside user control).
func TestShippedProvider_CleanupHookDoesNotShellInjectWorkdirOrBranch(t *testing.T) {
	prov := loadShippedProvider(t, "github-provider", "github")
	marker := filepath.Join(t.TempDir(), "pwned")
	maliciousBranch := `issue/1"; touch ` + marker + `; echo "`

	tasks := map[string]*contract.TaskState{
		contract.WorkflowPseudoNodeID: {
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"workdir": "/tmp/does-not-matter", "branch": maliciousBranch},
		},
	}
	vars := task.WorkflowHookVars{ResourceID: "https://github.com/acme/widgets/issues/1", SessionName: "acme/widgets-1"}
	_ = task.RunWorkflowCleanup(prov, vars, tasks, nil)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("branch was shell-interpreted: injected command executed")
	}
}

// TestShippedProvider_DeclaresAcquisitionAndRelease pins that the shipped
// provider owns both halves of the working-directory lifecycle, since a
// setup with no matching cleanup would strand every worktree it creates.
func TestShippedProvider_DeclaresAcquisitionAndRelease(t *testing.T) {
	prov := loadShippedProvider(t, "github-provider", "github")
	if prov.Setup == "" {
		t.Error("the shipped provider must declare setup")
	}
	if prov.Cleanup == "" {
		t.Error("the shipped provider must declare cleanup")
	}
}
