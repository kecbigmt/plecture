package pluginservice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// TestSupervisor_OverConfigLoadPlugins_RunsADeclaredService is the
// end-to-end proof for the plugin service lifecycle ADR's acceptance
// criteria: a plugin enabled through the normal catalog registration path,
// declaring a [[services]] entry, actually gets started by a Supervisor
// built the same way `plect serve` builds one — over config.LoadPlugins,
// not a hand-built PluginSource. The real shipped services (claude's
// channel-server, slack's slack-adapter) stay deliberately inert without a
// live session/Slack workspace behind them (see their own plugin.toml
// comments), so a synthetic fixture plugin exercises the same mechanism
// with an always-runnable service instead.
func TestSupervisor_OverConfigLoadPlugins_RunsADeclaredService(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PLECT_CONFIG_HOME", filepath.Join(tmpHome, ".config", "plect"))

	catalogDir := t.TempDir()
	scriptPath := filepath.Join(catalogDir, "svc", "bin", "svc")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\ntrap 'exit 0' TERM\nwhile true; do sleep 0.01; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, filepath.Join(catalogDir, "catalog.toml"), `
schema_version = 1
plugins = ["svc"]
`)
	writeIntegrationFile(t, filepath.Join(catalogDir, "svc", "plugin.toml"), `
schema_version = 1
plect_min_version = "0.0.0"

[[executables]]
name = "svc"
path = "bin/svc"

[[services]]
name = "svc"
executable = "svc"
`)

	writeIntegrationFile(t, filepath.Join(tmpHome, ".config", "plect", "catalogs.toml"), `
schema_version = 1

[[catalogs]]
alias = "local"
source = "path+editable://`+catalogDir+`"
plugins = ["svc"]
`)

	sup := newTestSupervisor(config.LoadPlugins)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		st, ok := sup.Status.Get("local/svc/svc")
		return ok && st.Running
	})
}

func writeIntegrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
