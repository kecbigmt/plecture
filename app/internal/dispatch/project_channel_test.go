package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
)

func TestDispatcher_AProjectChannelStopsOnceTheSessionsRootIsUnusable(t *testing.T) {
	for _, revoke := range []struct {
		name string
		do   func(t *testing.T, cfg *config.Config, root string)
	}{
		{"trust removed", func(t *testing.T, cfg *config.Config, root string) { cfg.TrustedProjectRoots = nil }},
		{"marker deleted", func(t *testing.T, cfg *config.Config, root string) {
			if err := os.Remove(filepath.Join(root, ".plect", "project.toml")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		for _, built := range []struct {
			name          string
			beforeRevoked bool
		}{
			{"dispatcher built while trusted", true},
			{"dispatcher built after revocation", false},
		} {
			t.Run(revoke.name+"/"+built.name, func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "delivered")
				cfg, root := projectChannelConfig(t, marker)
				log := eventlog.NewStore(t.TempDir())
				stateStore := state.NewStore(log.Dir())
				s := &domain.Session{Name: "session-1", Workflow: "standing", ProjectRoot: root}
				revision, err := cfg.CaptureWorkflowRevision(root, "standing")
				if err != nil {
					t.Fatal(err)
				}
				body, digest, err := revision.CanonicalBody()
				if err != nil {
					t.Fatal(err)
				}
				s.WorkflowRevision, s.WorkflowRevisionBody = digest, string(body)
				if err := stateStore.Put(s); err != nil {
					t.Fatal(err)
				}
				hub := sessionhub.NewRegistry(log)
				defer hub.Close()
				sup := NewSupervisor(func() *config.Config { return cfg }, stateStore, log, hub)

				var d *sessionDispatcher
				if built.beforeRevoked {
					d, _ = sup.buildDispatcher("session-1", s)
				}
				revoke.do(t, cfg, root)
				if !built.beforeRevoked {
					d, _ = sup.buildDispatcher("session-1", s)
				}
				if d == nil {
					return
				}
				d.policy = channel.RetryPolicy{MaxAttempts: 1, Timeout: 2 * time.Second}

				log.Append(event.Event{SessionName: "session-1", Type: event.TypeInstruction, Direction: event.Internal})
				drainOnce(d, s)

				if _, err := os.Stat(marker); err == nil {
					t.Fatal("the project channel ran after its root became unusable")
				}
				errs := channelErrors(t, log)
				if len(errs) != 1 || !strings.Contains(errs[0].Body, root) {
					t.Errorf("channel errors = %+v, want one naming %s", errs, root)
				}
			})
		}
	}
}

func TestDispatcher_AProjectChannelDeliversWhileTheRootIsTrusted(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "delivered")
	cfg, root := projectChannelConfig(t, marker)
	log := eventlog.NewStore(t.TempDir())
	stateStore := state.NewStore(log.Dir())
	s := &domain.Session{Name: "session-1", Workflow: "standing", ProjectRoot: root}
	revision, err := cfg.CaptureWorkflowRevision(root, "standing")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	s.WorkflowRevision, s.WorkflowRevisionBody = digest, string(body)
	if err := stateStore.Put(s); err != nil {
		t.Fatal(err)
	}
	hub := sessionhub.NewRegistry(log)
	defer hub.Close()
	d, _ := NewSupervisor(func() *config.Config { return cfg }, stateStore, log, hub).buildDispatcher("session-1", s)
	if d == nil {
		t.Fatal("no dispatcher built")
	}
	d.policy = channel.RetryPolicy{MaxAttempts: 1, Timeout: 2 * time.Second}

	log.Append(event.Event{SessionName: "session-1", Type: event.TypeInstruction, Direction: event.Internal})
	drainOnce(d, s)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the project channel did not deliver: %v", err)
	}
}

func projectChannelConfig(t *testing.T, marker string) (*config.Config, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "widgets")
	writeFile(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
	writeFile(t, filepath.Join(root, ".plect", "channels", "notify.toml"), `
[notify]
kind    = "channel"
type    = "exec"
command = "touch"
args    = ["`+marker+`"]
`)
	writeFile(t, filepath.Join(root, ".plect", "workflows", "standing.toml"), `
[standing]
kind     = "workflow"
resource = "issue"

[[standing.event.channel]]
name    = "notify"
uses    = "notify"
include = ["plect.instruction"]
`)
	return &config.Config{BaseDir: t.TempDir(), TrustedProjectRoots: []string{root}}, root
}
