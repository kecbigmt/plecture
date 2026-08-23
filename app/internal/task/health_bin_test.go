package task

import (
	"context"
	"strings"
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
	sourcePath := claude.Dir + "/config/tasks/runtime.toml"

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

// A probe whose value does not resolve never starts a process, and the health
// report has to hear about it the same way it hears about a failing execution:
// a probe that cannot be resolved is a probe that cannot answer. This pins the
// semantics docs/design/health-declaration.md states, which is where it drifted
// once already.
func TestRunProbes_UnresolvedValueSurfacesAsAProbeFailure(t *testing.T) {
	probe := Probe{Action: &lang.Action{
		Type:   lang.ActionShell,
		Script: `test -n "$missing"`,
		Bind:   map[string]*lang.Value{"missing": {Form: lang.FormFrom, From: "self.outputs.nope"}},
	}}
	session := SessionVars{Name: "s"}

	aliveErr := RunAliveProbe(context.Background(), probe, session)
	if aliveErr == nil || !strings.Contains(aliveErr.Error(), "resolved to nothing") {
		t.Fatalf("RunAliveProbe = %v, want an error naming the unresolved value", aliveErr)
	}
	signal, activityErr := RunActivityProbe(context.Background(), probe, session)
	if activityErr == nil || !strings.Contains(activityErr.Error(), "resolved to nothing") {
		t.Fatalf("RunActivityProbe err = %v, want an error naming the unresolved value", activityErr)
	}
	if signal != nil {
		t.Errorf("RunActivityProbe signal = %+v, want none", signal)
	}
}
