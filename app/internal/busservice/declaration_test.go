package busservice

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestBuildDeclarations_ResolvesExecPathAndContentHash(t *testing.T) {
	mounted := []plugins.Mounted{
		{
			ID:  "official/session/runtime",
			Dir: "/mnt/session-runtime",
			Manifest: plugins.Manifest{
				Executables: []plugins.Executable{
					{Name: "channel-server", Path: "bin/channel-server"},
				},
				Services: []plugins.Service{
					{
						Name:        "channel-server",
						Executable:  "channel-server",
						Args:        []string{"serve"},
						Env:         map[string]string{"LOG_LEVEL": "info"},
						RequiredEnv: []string{"SOME_TOKEN"},
						Restart:     plugins.ServiceRestartOnFailure,
						Health:      plugins.ServiceHealth{Type: plugins.ServiceHealthProcess},
					},
				},
			},
		},
	}
	lock := &plugins.Lockfile{
		Plugins: []plugins.PluginLockEntry{
			{ID: "official/session/runtime", ContentHash: "sha256:abc"},
		},
	}

	decls, err := BuildDeclarations(mounted, lock)
	if err != nil {
		t.Fatalf("BuildDeclarations: unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("decls = %+v, want 1", decls)
	}
	d := decls[0]
	if d.ID != "official/session/runtime/channel-server" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.PluginID != "official/session/runtime" {
		t.Errorf("PluginID = %q", d.PluginID)
	}
	wantExec := filepath.Join("/mnt/session-runtime", "bin/channel-server")
	if d.ExecPath != wantExec {
		t.Errorf("ExecPath = %q, want %q", d.ExecPath, wantExec)
	}
	if len(d.Args) != 1 || d.Args[0] != "serve" {
		t.Errorf("Args = %+v", d.Args)
	}
	if d.Env["LOG_LEVEL"] != "info" {
		t.Errorf("Env = %+v", d.Env)
	}
	if len(d.RequiredEnv) != 1 || d.RequiredEnv[0] != "SOME_TOKEN" {
		t.Errorf("RequiredEnv = %+v", d.RequiredEnv)
	}
	if d.Restart != plugins.ServiceRestartOnFailure {
		t.Errorf("Restart = %q", d.Restart)
	}
	if d.HealthType != plugins.ServiceHealthProcess {
		t.Errorf("HealthType = %q", d.HealthType)
	}
	if d.ContentHash != "sha256:abc" {
		t.Errorf("ContentHash = %q, want sha256:abc", d.ContentHash)
	}
}

func TestBuildDeclarations_MultiplePluginsMultipleServices(t *testing.T) {
	mounted := []plugins.Mounted{
		{
			ID:  "official/github",
			Dir: "/mnt/github",
			Manifest: plugins.Manifest{
				Executables: []plugins.Executable{{Name: "github-watcher", Path: "bin/github-watcher"}},
				Services:    []plugins.Service{{Name: "github-watcher", Executable: "github-watcher"}},
			},
		},
		{
			ID:  "official/session/runtime",
			Dir: "/mnt/session-runtime",
			Manifest: plugins.Manifest{
				Executables: []plugins.Executable{
					{Name: "channel-server", Path: "bin/channel-server"},
					{Name: "slack-adapter", Path: "bin/slack-adapter"},
				},
				Services: []plugins.Service{
					{Name: "channel-server", Executable: "channel-server"},
					{Name: "slack-adapter", Executable: "slack-adapter"},
				},
			},
		},
	}

	decls, err := BuildDeclarations(mounted, &plugins.Lockfile{})
	if err != nil {
		t.Fatalf("BuildDeclarations: unexpected error: %v", err)
	}
	if len(decls) != 3 {
		t.Fatalf("decls = %+v, want 3", decls)
	}
	ids := map[string]bool{}
	for _, d := range decls {
		ids[d.ID] = true
	}
	for _, want := range []string{
		"official/github/github-watcher",
		"official/session/runtime/channel-server",
		"official/session/runtime/slack-adapter",
	} {
		if !ids[want] {
			t.Errorf("missing declaration %q in %v", want, ids)
		}
	}
}

func TestBuildDeclarations_NilLockLeavesContentHashEmpty(t *testing.T) {
	mounted := []plugins.Mounted{
		{
			ID:  "local/dev",
			Dir: "/mnt/dev",
			Manifest: plugins.Manifest{
				Executables: []plugins.Executable{{Name: "x", Path: "bin/x"}},
				Services:    []plugins.Service{{Name: "x", Executable: "x"}},
			},
		},
	}

	decls, err := BuildDeclarations(mounted, nil)
	if err != nil {
		t.Fatalf("BuildDeclarations: unexpected error: %v", err)
	}
	if len(decls) != 1 || decls[0].ContentHash != "" {
		t.Fatalf("decls = %+v, want one declaration with empty ContentHash", decls)
	}
}

// TestBuildDeclarations_ExecutableNotFound_ReturnsError exercises a defensive
// branch that plugins.LoadManifest's own validation should already make
// unreachable through the normal load path (a service's executable must
// name one of the same plugin's own [[executables]]) — this hand-built
// Mounted bypasses that validation to prove BuildDeclarations still fails
// loud rather than mounting a service with an empty ExecPath.
func TestBuildDeclarations_ExecutableNotFound_ReturnsError(t *testing.T) {
	mounted := []plugins.Mounted{
		{
			ID:  "broken/plugin",
			Dir: "/mnt/broken",
			Manifest: plugins.Manifest{
				Services: []plugins.Service{{Name: "svc", Executable: "missing"}},
			},
		},
	}

	if _, err := BuildDeclarations(mounted, &plugins.Lockfile{}); err == nil {
		t.Fatal("want error when a service's executable is not declared, got nil")
	}
}
