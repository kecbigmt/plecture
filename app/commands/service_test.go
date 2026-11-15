package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/pluginservice"
)

func TestServiceListCommandExists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"service", "list"})
	if err != nil || cmd.Name() != "list" || cmd.Parent().Name() != "service" {
		t.Fatalf("service list command = %v, err = %v", cmd, err)
	}
	if cmd.Flags().Lookup("all") == nil {
		t.Fatal("service list --all is missing")
	}
}

func TestServiceListReadsOperatorDeclarationsAndAvailableExecutables(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	catalog := filepath.Join(home, "catalog")
	t.Setenv("PLECT_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	writeTestFile(t, filepath.Join(configHome, "config.toml"), "schema_version = 2\n")
	writeTestFile(t, filepath.Join(catalog, "catalog.toml"), "schema_version = 2\nplugins = [\"work\"]\n")
	writeTestFile(t, filepath.Join(catalog, "work", "plugin.toml"), `schema_version = 3
plect_min_version = "0.0.0"
[[executables]]
name = "worker"
path = "bin/worker"
service = true
[executables.config_schema]
type = "object"
[[executables]]
name = "spare"
path = "bin/spare"
service = true
[executables.config_schema]
type = "object"
`)
	writeTestFile(t, filepath.Join(configHome, "catalogs.toml"), "schema_version = 2\n[[catalogs]]\nalias = \"local\"\nsource = \"path+editable://"+catalog+"\"\nplugins = [\"work\"]\n")
	envPath := filepath.Join(home, "worker.env")
	if err := os.WriteFile(envPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(configHome, "services", "worker.toml"), "[[services]]\nname = \"worker\"\nexecutable = \"local/work/worker\"\nenv_file = \""+envPath+"\"\n")
	dataHome := filepath.Join(home, "data")
	t.Setenv("PLECT_DATA_HOME", dataHome)
	out, err := execRoot(t, "service", "list", "--all")
	t.Cleanup(func() { serviceListAll = false })
	if err != nil {
		t.Fatalf("service list: %v; output: %s", err, out)
	}
	for _, want := range []string{"worker", "local/work/worker", "stopped", "local/work/spare", "available"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
}

func TestServiceRowsShowsDeclaredAndAvailableExecutables(t *testing.T) {
	mounted := []plugins.Mounted{{ID: "local/work", Manifest: plugins.Manifest{Executables: []plugins.Executable{
		{Name: "worker", Service: true}, {Name: "spare", Service: true}, {Name: "action"},
	}}}}
	decls := []pluginservice.Declaration{{ID: "worker", Name: "worker", Executable: "local/work/worker"}}
	statuses := []pluginservice.Status{{ID: "worker", Running: true, PID: 123, RestartCount: 2}}
	rows := serviceRows(mounted, decls, statuses, true)
	if len(rows) != 2 || rows[0].Name != "worker" || rows[0].Executable != "local/work/worker" || rows[0].State != "running" || rows[0].PID != 123 || rows[0].Restarts != 2 || rows[1].Executable != "local/work/spare" || rows[1].State != "available" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows := serviceRows(mounted, decls, statuses, false); len(rows) != 1 {
		t.Fatalf("without --all = %+v", rows)
	}
}

func TestServiceRowsSortsNamedServicesBeforeAvailable(t *testing.T) {
	decls := []pluginservice.Declaration{
		{ID: "zeta", Name: "zeta", Executable: "local/work/zeta"},
		{ID: "alpha", Name: "alpha", Executable: "local/work/alpha"},
	}
	mounted := []plugins.Mounted{{ID: "local/work", Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "spare", Service: true}}}}}
	rows := serviceRows(mounted, decls, nil, true)
	if rows[0].Name != "alpha" || rows[1].Name != "zeta" || rows[2].Executable != "local/work/spare" {
		t.Fatalf("rows = %+v", rows)
	}
}
