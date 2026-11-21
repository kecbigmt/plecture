package commands

import (
	"bytes"
	"log/slog"
	"os"
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

func writeServeDefinition(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serveQueriesConfig(t *testing.T, subscribeBin, populationQuery string) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	host := filepath.Join(dir, "host")
	if err := os.MkdirAll(host, 0o755); err != nil {
		t.Fatal(err)
	}
	writeServeDefinition(t, host, "thread", `[thread]
kind = "resource"
match = "^urn:thread:(?P<id>[a-z0-9]+)$"
name = { from = "match.id" }
[thread.observe]
type = "exec"
command = "true"
[thread.query.subscribe]
type = "exec"
bin = "`+subscribeBin+`"
[thread.query.inputs_schema]
type = "object"
required = ["channel"]
additionalProperties = false
[thread.query.inputs_schema.properties.channel]
type = "string"
[thread.query.item_schema]
type = "object"
required = ["resource"]
additionalProperties = false
[thread.query.item_schema.properties.resource]
type = "string"
`)
	writeServeDefinition(t, host, "runtime", `[runtime]
kind = "effect"
scope = "run"
[runtime.setup]
type = "exec"
command = "true"
[runtime.health.alive]
type = "noop"
`)
	writeServeDefinition(t, host, "agent", `[agent]
kind = "workflow"
resource = "thread"
[agent.inputs_schema]
type = "object"
required = ["resource"]
additionalProperties = false
[agent.inputs_schema.properties.resource]
type = "string"
[[agent.nodes]]
id = "runtime"
uses = "runtime"
[[agent.populations]]
name = "mentions"
uses = ["subscribe"]
expire_after = "1h"
[agent.populations.query]
`+populationQuery+`
[agent.populations.session.inputs]
resource = { from = "resource.id" }
`)
	mount := func(name string) plugins.Mounted {
		return plugins.Mounted{ID: "local/" + name, Dir: filepath.Join(dir, name), Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "adapter", Path: "adapter", Service: true}}}}
	}
	return &config.Config{BaseDir: host, Plugins: []plugins.Mounted{mount("used"), mount("unrelated")}}, dir
}

func TestServiceQueriesWhenPopulationDefinitionsFailToLoad(t *testing.T) {
	const invalidQuery = "channel = 7"
	tests := []struct {
		name         string
		subscribeBin string
		query        string
		service      string
		wantHeld     bool
		wantItems    []pluginservice.QueryRegistration
		wantLog      string
	}{
		{
			name:         "a service no subscribe population uses starts with an empty snapshot and logs the failing definition",
			subscribeBin: "local/used/adapter", query: invalidQuery, service: "unrelated",
			wantItems: []pluginservice.QueryRegistration{}, wantLog: "agent.populations",
		},
		{
			name:         "a service a subscribe population uses is held and says why",
			subscribeBin: "local/used/adapter", query: invalidQuery, service: "used",
			wantHeld: true, wantLog: "agent.populations",
		},
		{
			name:         "every service is held when resource definitions fail to load",
			subscribeBin: "local/missing/adapter", query: `channel = "C1"`, service: "unrelated",
			wantHeld: true, wantLog: "local/missing/adapter",
		},
		{
			name:         "a loadable configuration still yields the exact snapshot",
			subscribeBin: "local/used/adapter", query: `channel = "C1"`, service: "used",
			wantItems: []pluginservice.QueryRegistration{{ID: "agent/mentions", Inputs: map[string]any{"channel": "C1"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, dir := serveQueriesConfig(t, tt.subscribeBin, tt.query)
			var log bytes.Buffer
			decl := pluginservice.Declaration{ID: "local/" + tt.service + "/adapter", ExecPath: filepath.Join(dir, tt.service, "adapter")}
			got, err := serviceQueries(cfg, "", decl, slog.New(slog.NewTextHandler(&log, nil)))
			if tt.wantHeld {
				if err == nil || !strings.Contains(err.Error(), tt.wantLog) {
					t.Fatalf("error = %v, want a hold naming %q", err, tt.wantLog)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want the service to start", err)
			}
			if !reflect.DeepEqual(got, tt.wantItems) {
				t.Fatalf("registrations = %#v, want %#v", got, tt.wantItems)
			}
			if tt.wantLog != "" && (!strings.Contains(log.String(), tt.wantLog) || !strings.Contains(log.String(), decl.ID)) {
				t.Fatalf("log = %s, want the service and %q", log.String(), tt.wantLog)
			}
		})
	}
}
