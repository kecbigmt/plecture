package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestUp_ResumeNoticeForRecreatedRun(t *testing.T) {
	for _, tt := range []struct {
		name, configured, wantBody string
		sleep                      bool
		queuedWake                 bool
	}{
		{name: "default", wantBody: config.DefaultResumeNotice},
		{name: "override", configured: "Continue after checking prior work.", wantBody: "Continue after checking prior work."},
		{name: "disabled", configured: "disabled"},
		{name: "wake", wantBody: config.DefaultResumeNotice, sleep: true},
		{name: "queued wake", wantBody: config.DefaultResumeNotice, sleep: true, queuedWake: true},
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
			if _, err := Down(cfg, store, DownParams{Identifier: name, Sleep: tt.sleep}); err != nil {
				t.Fatal(err)
			}
			if tt.queuedWake {
				requested, err := RequestWake(store, name, time.Now())
				if err != nil || !requested {
					t.Fatalf("request wake = (%t, %v)", requested, err)
				}
				if err := WakeQueuedSessions(cfg, store); err != nil {
					t.Fatal(err)
				}
			} else if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if tt.wantBody == "" {
				want = 0
			}
			trigger := "up"
			if tt.sleep {
				trigger = "wake"
			}
			check(want, trigger)
			if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
				t.Fatal(err)
			}
			check(want, trigger)
			if s := store.Get(name); s.Status != contract.SessionStatusUp {
				t.Fatalf("status = %q", s.Status)
			}
		})
	}
}

func TestUp_SidecarRecreatedWithoutAgentDoesNotPublishResumeNotice(t *testing.T) {
	store := testStore(t)
	flagsDir := t.TempDir()
	sidecarFlag := filepath.Join(flagsDir, "sidecar-unhealthy")
	agentFlag := filepath.Join(flagsDir, "agent-unhealthy")
	cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{
		{id: "agent", scope: "run", setup: `echo '{"pid":123,"session_id":"prior"}'`, cleanup: "true", alive: "test ! -e " + agentFlag, activity: `echo '{"fingerprint":"agent"}'`},
		{id: "sidecar", scope: "run", setup: `echo '{"pid":456,"mirror_endpoint":"mirror"}'`, cleanup: "true", alive: "test ! -e " + sidecarFlag, activity: `echo '{"fingerprint":"sidecar"}'`},
	}, []nodeFixture{{id: "agent"}, {id: "sidecar"}})
	addWorkflowFields(t, cfg, "default", `
[[default.event.channel]]
name = "mirror"
uses = "delivery"
include = ["plect.instruction"]
[default.event.channel.inputs]
endpoint = { from = "nodes.sidecar.outputs.mirror_endpoint" }
`)
	name := "sidecar-recreate"
	seedSessionWithNodes(t, store, name, "acct", 21, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	before := store.Get(name)
	if err := os.WriteFile(sidecarFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	after := store.Get(name)
	if !after.Nodes["agent"].SetupAt.Equal(before.Nodes["agent"].SetupAt) || !after.Nodes["sidecar"].SetupAt.After(before.Nodes["sidecar"].SetupAt) {
		t.Fatalf("setup times: before agent=%v sidecar=%v, after agent=%v sidecar=%v", before.Nodes["agent"].SetupAt, before.Nodes["sidecar"].SetupAt, after.Nodes["agent"].SetupAt, after.Nodes["sidecar"].SetupAt)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionResumed}})
	if err != nil || len(evs) != 0 {
		t.Fatalf("resume notices after sidecar recreation = %+v, %v; want none", evs, err)
	}
	if err := os.Remove(sidecarFlag); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	evs, _, _, err = eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionResumed}})
	if err != nil || len(evs) != 1 || evs[0].Metadata["prev_session_id"] != "prior" {
		t.Fatalf("resume notices after agent recreation = %+v, %v; want one for prior agent", evs, err)
	}
}

func TestUp_InstructionChannelIdentifiesAgentWithoutConversationID(t *testing.T) {
	store := testStore(t)
	agentFlag := filepath.Join(t.TempDir(), "agent-unhealthy")
	cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{
		{id: "agent", scope: "run", setup: `echo '{"pid":123,"queue_dir":"agent-queue"}'`, cleanup: "true", alive: "test ! -e " + agentFlag, activity: `echo '{"fingerprint":"agent"}'`},
		{id: "sidecar", scope: "run", setup: `echo '{"pid":456}'`, cleanup: "true", activity: `echo '{"fingerprint":"sidecar"}'`},
	}, []nodeFixture{{id: "agent"}, {id: "sidecar"}})
	addWorkflowFields(t, cfg, "default", `
[[default.event.channel]]
name = "runtime"
uses = "delivery"
include = ["plect.instruction"]
[default.event.channel.inputs]
queue_dir = { from = "nodes.agent.outputs.queue_dir" }
`)
	name := "channel-agent-recreate"
	seedSessionWithNodes(t, store, name, "acct", 22, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	evs, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionResumed}})
	if err != nil || len(evs) != 1 {
		t.Fatalf("resume notices after agent recreation = %+v, %v; want one", evs, err)
	}
	if _, ok := evs[0].Metadata["prev_session_id"]; ok {
		t.Fatalf("resume metadata = %+v; no prior conversation ID was available", evs[0].Metadata)
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
