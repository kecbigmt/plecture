package pluginservice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

func TestStatusRegistry_UpdateAndGet(t *testing.T) {
	reg := NewStatusRegistry()

	reg.Update("plugin/svc", func(st *Status) {
		st.ID = "plugin/svc"
		st.PluginID = "plugin"
		st.Name = "svc"
		st.Running = true
		st.PID = 123
		st.Health = domain.HealthHealthy
	})

	st, ok := reg.Get("plugin/svc")
	if !ok {
		t.Fatal("Get: want an entry for plugin/svc")
	}
	if !st.Running || st.PID != 123 || st.Health != domain.HealthHealthy {
		t.Fatalf("Status = %+v", st)
	}
}

func TestReadStatusSnapshotTreatsExitedSupervisorAsStopped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(path, []byte(`{"owner_pid":99999999,"statuses":[{"ID":"worker","Name":"worker","Running":true,"PID":123}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatusSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Running || got[0].PID != 0 {
		t.Fatalf("stale snapshot = %+v", got)
	}
}

func TestStatusSnapshotCanBeReadByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	reg := NewStatusRegistry()
	reg.SetSnapshotPath(path)
	reg.Update("worker", func(st *Status) {
		st.ID = "worker"
		st.Name = "worker"
		st.Running = true
		st.PID = 123
		st.RestartCount = 2
	})
	got, err := ReadStatusSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "worker" || got[0].PID != 123 || got[0].RestartCount != 2 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestStatusRegistry_UpdateIsReadModifyWrite(t *testing.T) {
	reg := NewStatusRegistry()

	reg.Update("plugin/svc", func(st *Status) { st.RestartCount++ })
	reg.Update("plugin/svc", func(st *Status) { st.RestartCount++ })

	st, ok := reg.Get("plugin/svc")
	if !ok || st.RestartCount != 2 {
		t.Fatalf("Status = %+v, ok = %v, want RestartCount = 2", st, ok)
	}
}

func TestStatusRegistry_Get_UnknownID(t *testing.T) {
	reg := NewStatusRegistry()

	if _, ok := reg.Get("does/not-exist"); ok {
		t.Fatal("Get: want ok = false for an unknown id")
	}
}

func TestStatusRegistry_All_SortedByID(t *testing.T) {
	reg := NewStatusRegistry()
	reg.Update("b/svc", func(st *Status) { st.ID = "b/svc" })
	reg.Update("a/svc", func(st *Status) { st.ID = "a/svc" })

	all := reg.All()
	if len(all) != 2 || all[0].ID != "a/svc" || all[1].ID != "b/svc" {
		t.Fatalf("All() = %+v, want sorted by ID", all)
	}
}
