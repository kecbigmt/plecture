package task

import (
	"context"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// A `[health]` probe is one of the surfaces load-time validation accepts a
// plugin-local bare `{{bin "<name>"}}` on, so running one has to resolve that
// reference the same way setup and cleanup do — against the definition file's
// own plugin, not against whichever file happens to name the task.
func TestRunHealthProbes_ResolvePluginLocalBin(t *testing.T) {
	repoRoot := repoRootForTest(t)
	claude := mountShippedPlugin(t, repoRoot, "acme-mirror", "plugins/claude")
	session := SessionVars{Name: "s", WorkspaceDirPath: t.TempDir(), Plugins: []plugins.Mounted{claude}}
	sourcePath := claude.Dir + "/config/tasks/claude.toml"

	t.Run("alive", func(t *testing.T) {
		if err := RunAliveProbe(context.Background(), `test -n {{bin "claude-agent-activity" | shellQuote}}`, nil, nil, session, sourcePath); err != nil {
			t.Fatalf("RunAliveProbe: %v", err)
		}
	})

	t.Run("activity", func(t *testing.T) {
		probe := `printf '{"fingerprint":"%s"}\n' "$(basename {{bin "claude-agent-activity" | shellQuote}})"`
		sig, err := RunActivityProbe(context.Background(), probe, nil, nil, session, sourcePath)
		if err != nil {
			t.Fatalf("RunActivityProbe: %v", err)
		}
		if sig == nil || sig.Fingerprint != "claude-agent-activity" {
			t.Fatalf("signal = %#v", sig)
		}
	})

	t.Run("no source path still reports the unresolvable reference", func(t *testing.T) {
		if err := RunAliveProbe(context.Background(), `echo {{bin "claude-agent-activity"}}`, nil, nil, session, ""); err == nil {
			t.Fatal("want an error for a bare bin reference with no owning plugin file")
		}
	})
}
