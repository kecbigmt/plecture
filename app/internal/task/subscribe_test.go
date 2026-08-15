package task

import (
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestRunProviderSubscribe_TemplateVars(t *testing.T) {
	marker := "/tmp/subscribe-marker"
	prov := config.ProviderConfig{
		ID:        "wf",
		Subscribe: `echo "{{.ResourceID}} {{.SessionName}}" > ` + marker,
	}
	if err := RunProviderSubscribe(prov, SubscribeHookVars{ResourceID: "res-1", SessionName: "sess-1"}); err != nil {
		t.Fatalf("RunProviderSubscribe: %v", err)
	}
}

func TestRunProviderSubscribe_NoSubscribeHookIsAnError(t *testing.T) {
	err := RunProviderSubscribe(config.ProviderConfig{ID: "wf"}, SubscribeHookVars{ResourceID: "r", SessionName: "s"})
	if err == nil {
		t.Fatal("expected an error when the provider declares no subscribe hook")
	}
}

// TestRunProviderSubscribe_ResolvesBinReference pins that a provider's
// subscribe hook can invoke a plugin-shipped executable through
// `{{bin ...}}`, matching setup/cleanup (workflowhook.go) and task/resource
// hooks — a subscribe hook otherwise has only bare command names on `PATH`
// available to reach its own plugin's executables.
func TestRunProviderSubscribe_ResolvesBinReference(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "github-watcher", Path: "bin/github-watcher"})}
	prov := config.ProviderConfig{
		ID:        "wf",
		Subscribe: `echo {{bin "official/github/github-watcher"}} subscribe`,
	}
	if err := RunProviderSubscribe(prov, SubscribeHookVars{ResourceID: "r", SessionName: "s", Plugins: mounted}); err != nil {
		t.Fatalf("RunProviderSubscribe: %v", err)
	}
}

// TestRenderSubscribeHook_ResolvesBinReference exercises the renderer
// directly so the rendered command string (not just a successful shell run)
// can be asserted against.
func TestRenderSubscribeHook_ResolvesBinReference(t *testing.T) {
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "github-watcher", Path: "bin/github-watcher"})}
	got, err := renderSubscribeHook(`{{bin "official/github/github-watcher"}} subscribe`, SubscribeHookVars{ResourceID: "r", SessionName: "s", Plugins: mounted})
	if err != nil {
		t.Fatalf("renderSubscribeHook: %v", err)
	}
	want := filepath.Join("/mnt/github", "bin", "github-watcher") + " subscribe"
	if got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
}
