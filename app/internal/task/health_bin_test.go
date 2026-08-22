package task

import (
	"context"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// A `[health]` probe is one of the surfaces a plugin-local bare
// `bin = "<name>"` is accepted on, so running one has to resolve that
// reference the same way setup and cleanup do — against the declaration
// file's own plugin, not against whichever file happens to name the effect.
func TestRunHealthProbes_ResolvePluginLocalBin(t *testing.T) {
	repoRoot := repoRootForTest(t)
	claude := mountShippedPlugin(t, repoRoot, "acme-mirror", "plugins/claude")
	session := SessionVars{Name: "s", WorkspaceDirPath: t.TempDir(), Plugins: []plugins.Mounted{claude}}
	sourcePath := claude.Dir + "/config/tasks/claude.toml"

	activityBin := &lang.Value{Form: lang.FormBin, Bin: "claude-agent-activity"}

	t.Run("alive", func(t *testing.T) {
		probe := Probe{
			Action:     &lang.Action{Type: lang.ActionShell, Script: `test -n "$activity_bin"`, Bind: map[string]*lang.Value{"activity_bin": activityBin}},
			SourcePath: sourcePath,
		}
		if err := RunAliveProbe(context.Background(), probe, session); err != nil {
			t.Fatalf("RunAliveProbe: %v", err)
		}
	})

	t.Run("activity", func(t *testing.T) {
		probe := Probe{
			Action: &lang.Action{
				Type:   lang.ActionShell,
				Script: `printf '{"fingerprint":"%s"}\n' "$(basename "$activity_bin")"`,
				Bind:   map[string]*lang.Value{"activity_bin": activityBin},
			},
			SourcePath: sourcePath,
		}
		sig, err := RunActivityProbe(context.Background(), probe, session)
		if err != nil {
			t.Fatalf("RunActivityProbe: %v", err)
		}
		if sig == nil || sig.Fingerprint != "claude-agent-activity" {
			t.Fatalf("signal = %#v", sig)
		}
	})

	t.Run("no source path still reports the unresolvable reference", func(t *testing.T) {
		probe := Probe{Action: &lang.Action{Type: lang.ActionExec, Bin: "claude-agent-activity"}}
		if err := RunAliveProbe(context.Background(), probe, session); err == nil {
			t.Fatal("want an error for a bare bin reference with no owning plugin file")
		}
	})
}
