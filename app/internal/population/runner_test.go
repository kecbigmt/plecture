package population

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

func TestActionRunnerQueriesReceivePluginCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLECT_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("PLECT_CONFIG_HOME", dir)
	bin := filepath.Join(dir, "query")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = poll ]; then printf '[{\"resource\":\"%s\"}]\\n' \"$PLECT_CACHE_DIR\"; else printf '{\"resource\":\"%s\"}\\n' \"$PLECT_CACHE_DIR\"; fi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mounted := plugins.Mounted{ID: "local/query", Dir: dir, Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "query", Path: "query", Service: true, ConfigSchema: map[string]any{"type": "object"}}}}}
	runner := actionRunner{cfg: &config.Config{Plugins: []plugins.Mounted{mounted}}}
	def := Definition{Entry: config.ResourceDef{Query: &config.ResourceQuery{Poll: &lang.Action{Type: lang.ActionExec, Bin: "local/query/query", Args: []*lang.Value{{Form: lang.FormLiteral, Literal: "poll"}}}, Subscribe: &lang.Action{Type: lang.ActionExec, Bin: "local/query/query", Args: []*lang.Value{{Form: lang.FormLiteral, Literal: "subscribe"}}}}}}
	want := filepath.Join(dir, "cache", "plugins", "local", "query")
	items, err := runner.Poll(context.Background(), def)
	if err != nil || len(items) != 1 || items[0]["resource"] != want {
		t.Fatalf("poll = %v, %v; want %q", items, err, want)
	}
	var emitted string
	err = runner.Subscribe(context.Background(), def, func(item map[string]any) error {
		emitted, _ = item["resource"].(string)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "exited") || emitted != want {
		t.Fatalf("subscribe emitted %q, error %v; want %q and exit", emitted, err, want)
	}
}

func queryAction(command string, args ...string) *lang.Action {
	values := make([]*lang.Value, 0, len(args))
	for _, arg := range args {
		values = append(values, &lang.Value{Form: lang.FormLiteral, Literal: arg})
	}
	return &lang.Action{Type: lang.ActionExec, Command: command, Args: values}
}

func TestActionRunnerPollRequiresAJSONArray(t *testing.T) {
	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Entry: config.ResourceDef{Query: &config.ResourceQuery{}}}
	for _, output := range []string{`{"resource":"urn:case:a"}`, `null`} {
		def.Entry.Query.Poll = queryAction("printf", output)
		_, err := runner.Poll(context.Background(), def)
		if err == nil || !strings.Contains(err.Error(), "JSON item array") {
			t.Fatalf("output %s: error = %v, want JSON item array failure", output, err)
		}
	}

	def.Entry.Query.Poll = queryAction("printf", `[{"resource":"urn:case:a"}]`)
	items, err := runner.Poll(context.Background(), def)
	if err != nil || len(items) != 1 || items[0]["resource"] != "urn:case:a" {
		t.Fatalf("items = %v, error = %v", items, err)
	}
}

func TestActionRunnerPollStripsPlectDataHomeButKeepsXDGDataHome(t *testing.T) {
	t.Setenv("PLECT_DATA_HOME", "/poisoned")
	t.Setenv("XDG_DATA_HOME", "/still-inherited")

	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Entry: config.ResourceDef{Query: &config.ResourceQuery{
		Poll: &lang.Action{Type: lang.ActionShell, Script: `
plect_set=no; [ -n "$PLECT_DATA_HOME" ] && plect_set=yes
xdg_set=no; [ -n "$XDG_DATA_HOME" ] && xdg_set=yes
printf '[{"resource":"plect=%s xdg=%s"}]' "$plect_set" "$xdg_set"
`},
	}}}
	items, err := runner.Poll(context.Background(), def)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != 1 || items[0]["resource"] != "plect=no xdg=yes" {
		t.Fatalf("poll subprocess env = %v, want PLECT_DATA_HOME stripped and XDG_DATA_HOME kept", items)
	}
}

func TestActionRunnerPollStripsPlectCacheHomeButKeepsXDGCacheHome(t *testing.T) {
	t.Setenv("PLECT_CACHE_HOME", "/poisoned-cache")
	t.Setenv("XDG_CACHE_HOME", "/still-inherited-cache")

	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Entry: config.ResourceDef{Query: &config.ResourceQuery{
		Poll: &lang.Action{Type: lang.ActionShell, Script: `
plect_set=no; [ -n "$PLECT_CACHE_HOME" ] && plect_set=yes
xdg_set=no; [ -n "$XDG_CACHE_HOME" ] && xdg_set=yes
printf '[{"resource":"plect=%s xdg=%s"}]' "$plect_set" "$xdg_set"
`},
	}}}
	items, err := runner.Poll(context.Background(), def)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(items) != 1 || items[0]["resource"] != "plect=no xdg=yes" {
		t.Fatalf("poll subprocess env = %v, want PLECT_CACHE_HOME stripped and XDG_CACHE_HOME kept", items)
	}
}

func TestActionRunnerSubscribeEmitsOneItemPerLine(t *testing.T) {
	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Entry: config.ResourceDef{Query: &config.ResourceQuery{
		Subscribe: queryAction("printf", "%s\n%s\n", `{"resource":"urn:case:a"}`, `{"resource":"urn:case:b"}`),
	}}}
	var resources []string
	err := runner.Subscribe(context.Background(), def, func(item map[string]any) error {
		resources = append(resources, item["resource"].(string))
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("termination error = %v, want supervised-restart signal", err)
	}
	if strings.Join(resources, ",") != "urn:case:a,urn:case:b" {
		t.Fatalf("resources = %v", resources)
	}
}
