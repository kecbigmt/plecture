package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestRunProviderSubscribe_ReadsItsSurfaceRoots(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "subscribe.out")
	prov := config.WorkspaceProviderConfig{
		ID: "wf",
		Subscribe: &lang.Action{
			Type:    lang.ActionExec,
			Command: "sh",
			Args: []*lang.Value{
				{Form: lang.FormLiteral, Literal: "-c"},
				{Form: lang.FormLiteral, Literal: `printf '%s %s' "$1" "$2" > "$3"`},
				{Form: lang.FormLiteral, Literal: "subscribe"},
				{Form: lang.FormFrom, From: "resource.id"},
				{Form: lang.FormFrom, From: "session.name"},
				{Form: lang.FormLiteral, Literal: marker},
			},
		},
	}
	if err := RunWorkspaceProviderSubscribe(prov, SubscribeHookVars{ResourceID: "res-1", SessionName: "sess-1"}); err != nil {
		t.Fatalf("RunWorkspaceProviderSubscribe: %v", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "res-1 sess-1" {
		t.Errorf("subscribe saw %q, want the resource and session", got)
	}
}

func TestRunProviderSubscribe_NoSubscribeHookIsAnError(t *testing.T) {
	err := RunWorkspaceProviderSubscribe(config.WorkspaceProviderConfig{ID: "wf"}, SubscribeHookVars{ResourceID: "r", SessionName: "s"})
	if err == nil {
		t.Fatal("expected an error when the provider declares no subscribe hook")
	}
}

// A subscribe hook reaches its own plugin's executables through `bin` — it
// otherwise has only bare command names on PATH available.
func TestRunProviderSubscribe_ResolvesBinReference(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "bin.out")
	mounted := []plugins.Mounted{mustMount("official/github", "/mnt/github",
		plugins.Executable{Name: "github-watcher", Path: "bin/github-watcher"})}
	prov := config.WorkspaceProviderConfig{
		ID: "wf",
		Subscribe: &lang.Action{
			Type:    lang.ActionExec,
			Command: "sh",
			Args: []*lang.Value{
				{Form: lang.FormLiteral, Literal: "-c"},
				{Form: lang.FormLiteral, Literal: `printf '%s' "$1" > "$2"`},
				{Form: lang.FormLiteral, Literal: "subscribe"},
				{Form: lang.FormBin, Bin: "official/github/github-watcher"},
				{Form: lang.FormLiteral, Literal: marker},
			},
		},
	}
	if err := RunWorkspaceProviderSubscribe(prov, SubscribeHookVars{ResourceID: "r", SessionName: "s", Plugins: mounted}); err != nil {
		t.Fatalf("RunWorkspaceProviderSubscribe: %v", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/mnt/github", "bin", "github-watcher")
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Errorf("resolved executable = %q, want %q", got, want)
	}
}
