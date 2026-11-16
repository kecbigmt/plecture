package commands

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/pluginservice"
	"github.com/kecbigmt/plecture/app/internal/population"
)

func TestServiceQueryRegistrationsSelectsOnlySubscribeQueriesForService(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Plugins: []plugins.Mounted{
		{ID: "local/first", Dir: filepath.Join(dir, "first"), Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "adapter", Path: "adapter"}}}},
		{ID: "local/second", Dir: filepath.Join(dir, "second"), Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "adapter", Path: "adapter"}}}},
	}}
	makeDef := func(address, name string, uses []string, bin string) population.Definition {
		return population.Definition{
			Workflow:   config.WorkflowFile{Address: address},
			Population: config.WorkflowPopulation{Name: name, Uses: uses, Query: map[string]any{"channel_ids": []string{"C1"}}},
			Entry:      config.ResourceDef{Query: &config.ResourceQuery{Subscribe: &lang.Action{Bin: bin}}},
		}
	}
	defs := []population.Definition{
		makeDef("team/flow", "mentions", []string{"subscribe"}, "local/first/adapter"),
		makeDef("team/flow", "poll", []string{"poll"}, "local/first/adapter"),
		makeDef("other/flow", "mentions", []string{"subscribe"}, "local/second/adapter"),
		makeDef("team/flow", "missing-action", []string{"subscribe"}, ""),
	}
	got, err := serviceQueryRegistrations(cfg, defs, pluginservice.Declaration{ExecPath: filepath.Join(dir, "first", "adapter")})
	if err != nil {
		t.Fatal(err)
	}
	want := []pluginservice.QueryRegistration{{ID: "team/flow/mentions", Inputs: map[string]any{"channel_ids": []string{"C1"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registrations = %#v, want %#v", got, want)
	}
}

func TestServiceQueryRegistrationsRejectsUnresolvedSubscribeBin(t *testing.T) {
	defs := []population.Definition{{
		Workflow:   config.WorkflowFile{Address: "team/flow"},
		Population: config.WorkflowPopulation{Name: "mentions", Uses: []string{"subscribe"}},
		Entry:      config.ResourceDef{Query: &config.ResourceQuery{Subscribe: &lang.Action{Bin: "local/missing/adapter"}}},
	}}
	_, err := serviceQueryRegistrations(&config.Config{}, defs, pluginservice.Declaration{ExecPath: "/adapter"})
	if err == nil || !strings.Contains(err.Error(), "no mounted plugin") {
		t.Fatalf("resolution error = %v", err)
	}
}
