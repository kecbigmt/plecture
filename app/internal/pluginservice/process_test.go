package pluginservice

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/persistence"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestRunProcessConfigFirstAndCorrelatedPublishReply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLECT_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("PLECT_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("UNRELATED_SECRET", "must-not-leak")
	output := filepath.Join(dir, "observed")
	script := writeScript(t, dir, "worker", "read -r config\nread -r subscriptions\necho '{\"op\":\"publish\",\"id\":\"one\",\"resource\":\"item://one\",\"type\":\"user.note\"}'\nread -r reply\nprintf '%s\\n%s\\n%s\\n%s\\n%s\\n' \"$config\" \"$subscriptions\" \"$reply\" \"$PLECT_DATA_DIR\" \"$PLECT_CACHE_DIR\" > \""+output+"\"\nif [ -n \"$UNRELATED_SECRET\" ]; then exit 9; fi\necho 'stderr marker' >&2\n")
	var log bytes.Buffer
	decl := Declaration{ID: "worker", PluginID: "local/work", Name: "worker", ExecPath: script, Config: map[string]any{"region": "west"}}
	protocol := &Protocol{
		Subscriptions: func(context.Context) ([]persistence.Subscription, error) { return nil, nil },
		Publish: func(_ context.Context, req PublishRequest) (int, error) {
			if req.ID != "one" || req.Resource != "item://one" {
				t.Errorf("request = %+v", req)
			}
			return 2, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runProcess(ctx, decl, slog.New(slog.NewTextHandler(&log, nil)), time.Second, func(int) {}, protocol); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 5 || !strings.Contains(lines[0], `"op":"config"`) || !strings.Contains(lines[0], `"region":"west"`) || !strings.Contains(lines[1], `"op":"subscriptions"`) || !strings.Contains(lines[2], `"op":"published"`) || !strings.Contains(lines[2], `"id":"one"`) || !strings.Contains(lines[2], `"delivered":2`) {
		t.Fatalf("messages = %q", lines)
	}
	if lines[3] != filepath.Join(dir, "data", "plugins", "local", "work") || lines[4] != filepath.Join(dir, "cache", "plugins", "local", "work") {
		t.Fatalf("directories = %q", lines)
	}
	if !strings.Contains(log.String(), "stderr marker") {
		t.Fatalf("stderr = %s", log.String())
	}
}

func TestBuildEnvOnlyPrivateBindings(t *testing.T) {
	t.Setenv("UNRELATED_SECRET", "must-not-leak")
	env := buildEnv(map[string]string{"TOKEN": "value", "XDG_CACHE_HOME": "/private"}, "/data", "/cache")
	for _, want := range []string{"TOKEN=value", "XDG_CACHE_HOME=/private", "PLECT_DATA_DIR=/data", "PLECT_CACHE_DIR=/cache"} {
		if !slices.Contains(env, want) {
			t.Fatalf("env = %v, missing %s", env, want)
		}
	}
	if slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, "UNRELATED_SECRET=") }) {
		t.Fatalf("secret leaked: %v", env)
	}
}

func TestSubscriptionDeltasReportAddedAndRemovedRows(t *testing.T) {
	removed := persistence.Subscription{SessionID: "s1", Resource: contract.Resource{ID: "r1", Type: "issue"}, Source: "runtime"}
	added := persistence.Subscription{SessionID: "s2", Resource: contract.Resource{ID: "r2", Type: "issue"}, Source: "dispatch"}
	delta := subscriptionDeltas([]persistence.Subscription{removed}, []persistence.Subscription{added})["issue"]
	if len(delta.add) != 1 || delta.add[0].SessionID != "s2" || len(delta.remove) != 1 || delta.remove[0].SessionID != "s1" {
		t.Fatalf("delta = %+v", delta)
	}
}

func TestSubscriptionDeltasReportUpdatedInputs(t *testing.T) {
	before := persistence.Subscription{SessionID: "s1", Session: "work", Resource: contract.Resource{ID: "r1", Type: "issue"}, Source: "runtime", InputsJSON: `{"branch":"old"}`}
	after := before
	after.InputsJSON = `{"branch":"new"}`
	delta := subscriptionDeltas([]persistence.Subscription{before}, []persistence.Subscription{after})["issue"]
	if len(delta.remove) != 1 || len(delta.add) != 1 || delta.remove[0].InputsJSON != before.InputsJSON || delta.add[0].InputsJSON != after.InputsJSON {
		t.Fatalf("updated inputs delta = %+v", delta)
	}
}
