package busservice

import (
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
