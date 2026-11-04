package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestUp_ResumeNoticeForRecreatedRun(t *testing.T) {
	for _, tt := range []struct {
		name, configured, wantBody string
	}{
		{"default", "", config.DefaultResumeNotice},
		{"override", "Continue after checking prior work.", "Continue after checking prior work."},
		{"disabled", "disabled", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{id: "runtime", scope: "run", setup: `echo '{"session_id":"prior"}'`, cleanup: "true"}}, []nodeFixture{{id: "runtime"}})
			if tt.configured != "" {
				value := tt.configured
				if value == "disabled" {
					value = ""
				}
				path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				b = append(b, []byte("\n[default.health]\nauto_repair_attempts = 0\nresume_notice = \""+value+"\"\n")...)
				if err := os.WriteFile(path, b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			name := "work-20"
			seedSessionWithNodes(t, store, name, "acct", 20, "default", nil)
			check := func(want int, trigger string) {
				t.Helper()
				evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionResumed}})
				if err != nil || len(evs) != want {
					t.Fatalf("resumed events = %+v, %v; want %d", evs, err, want)
				}
				if want > 0 && (evs[0].Body != tt.wantBody || evs[0].Metadata["trigger"] != trigger || evs[0].Metadata["prev_session_id"] != "prior" || evs[0].Direction != event.Inbound) {
					t.Fatalf("resumed event = %+v", evs[0])
				}
			}
			if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			check(0, "")
			if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			check(0, "")
			if _, err := Down(cfg, store, DownParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if tt.wantBody == "" {
				want = 0
			}
			check(want, "up")
			if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			check(want, "up")
			if s := store.Get(name); s.Status != contract.SessionStatusUp {
				t.Fatalf("status = %q", s.Status)
			}
		})
	}
}

func TestRepairSession_PublishesResumeNotice(t *testing.T) {
	store := testStore(t)
	cfg := repairWorkflowFixture(t, `echo '{"session_id":"prior"}'`, "false", 2)
	name := "session-1"
	seedRepairSession(t, store, name, nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if attempted, err := RepairSession(cfg, store, name, &HealthReport{Repairable: true, Reason: "runtime stopped"}, NewRepairGate()); err != nil || !attempted {
		t.Fatalf("repair = (%t, %v)", attempted, err)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionResumed}})
	if err != nil || len(evs) != 1 {
		t.Fatalf("resumed events = %+v, %v", evs, err)
	}
	if evs[0].Metadata["trigger"] != "repair" || evs[0].Metadata["prev_session_id"] != "prior" {
		t.Fatalf("resume metadata = %+v", evs[0].Metadata)
	}
}
