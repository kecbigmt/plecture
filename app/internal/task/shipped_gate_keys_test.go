package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// A completion predicate is only as sound as its wiring to the facts it reads.
// Every fact a shipped gate reads has exactly one source — a key an observer
// publishes about the resource, a key something records into the instance, or
// a judge's verdict — and which one it is decides where that fact lives once
// the completion surface moves onto its own roots.
//
// This records that mapping per plugin, from the shipped declarations, and
// asserts the part of it that can be wrong on its own: an observed fact no
// observer publishes is a gate that can never satisfy. (That a check reads a
// declared key, and that the key is in the outputs contract, is
// ValidateTaskRequires' rule and is enforced at load.)
//
// A derived fact records as one line here because its operands live inside a
// script. Once a derived comparison becomes an expression over the two live
// roots, its operands are declarations and the record names them — which is
// the whole of what moving this surface buys for such a fact.
//
// Set PLECT_UPDATE_GATE_RECORDS=1 to rewrite the records after an intended
// change.

const gateRecordName = "gate-keys.txt"

// factSource is where one gate fact's value comes from.
type factSource string

const (
	// observed names a key a resource observer publishes about the resource.
	observed factSource = "observed"
	// recorded names a key something outside this instance writes into it.
	recorded factSource = "recorded"
	// derived names a value this declaration computes from other facts.
	derived factSource = "derived"
	// judged names a reviewer's verdict against a judge leaf.
	judged factSource = "judged"
)

type gateFact struct {
	Name   string
	Source factSource
	// Publishers are the observers that publish an observed fact.
	Publishers []string
	// Where says which predicate reads it: the completion predicate, a
	// chain's trigger, or both.
	Where string
}

func TestShippedGates_KeyMappingMatchesTheRecord(t *testing.T) {
	tasks, _ := loadShippedCatalogTasks(t)
	observers := loadShippedCatalogObservers(t)

	byPlugin := map[string][]string{}
	for id, def := range tasks {
		if def.DoneWhen == nil && len(def.Chains) == 0 {
			continue
		}
		byPlugin[pluginRootOf(def.SourcePath)] = append(byPlugin[pluginRootOf(def.SourcePath)], id)
	}
	if len(byPlugin) == 0 {
		t.Fatal("no shipped declaration carries a gate; this test is checking nothing")
	}

	for root, ids := range byPlugin {
		sort.Strings(ids)
		t.Run(filepath.Base(root), func(t *testing.T) {
			var b strings.Builder
			for _, id := range ids {
				facts := gateFactsOf(tasks[id], observers)
				fmt.Fprintf(&b, "== %s\n", id)
				for _, f := range facts {
					if f.Source == observed && len(f.Publishers) == 0 {
						t.Errorf("%s: gate reads %q as an observed fact, but no shipped observer publishes it", id, f.Name)
					}
					line := fmt.Sprintf("%-18s %-9s %s", f.Name, f.Source, f.Where)
					if len(f.Publishers) > 0 {
						line += "   " + strings.Join(f.Publishers, ", ")
					}
					b.WriteString(strings.TrimRight(line, " ") + "\n")
				}
				b.WriteString("\n")
			}
			record := filepath.Join(root, "testdata", gateRecordName)
			produced := strings.TrimRight(b.String(), "\n") + "\n"
			if os.Getenv("PLECT_UPDATE_GATE_RECORDS") == "1" {
				if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, []byte(produced), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("rewrote %s", record)
				return
			}
			want, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("a plugin whose declarations carry gates records where each gate fact comes from: %v", err)
			}
			if string(want) != produced {
				t.Errorf("gate key mapping changed.\n--- recorded\n%s\n--- produced\n%s", want, produced)
			}
		})
	}
}

// gateFactsOf classifies every fact one declaration's gates read, in a stable
// order.
func gateFactsOf(def config.TaskDefinition, observers map[string][]string) []gateFact {
	where := map[string]map[string]bool{}
	note := func(name, predicate string) {
		if where[name] == nil {
			where[name] = map[string]bool{}
		}
		where[name][predicate] = true
	}
	sources := map[string]factSource{}
	if def.DoneWhen != nil {
		for _, leaf := range def.DoneWhen.All {
			if leaf.Judge != "" {
				sources[leaf.ID] = judged
				note(leaf.ID, "done_when")
				continue
			}
			sources[leaf.Check] = factSourceOf(def, leaf.Check, observers)
			note(leaf.Check, "done_when")
		}
	}
	for _, ch := range def.Chains {
		for _, fact := range ch.When.All {
			switch {
			case fact.JudgePending != "":
				sources[fact.JudgePending] = judged
				note(fact.JudgePending, "chain."+ch.ID)
			case fact.JudgeAction != "":
				sources[fact.JudgeAction] = judged
				note(fact.JudgeAction, "chain."+ch.ID)
			default:
				sources[fact.Check] = factSourceOf(def, fact.Check, observers)
				note(fact.Check, "chain."+ch.ID)
			}
		}
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]gateFact, 0, len(names))
	for _, name := range names {
		predicates := make([]string, 0, len(where[name]))
		for p := range where[name] {
			predicates = append(predicates, p)
		}
		sort.Strings(predicates)
		f := gateFact{Name: name, Source: sources[name], Where: strings.Join(predicates, "+")}
		if f.Source == observed {
			f.Publishers = observers[name]
		}
		out = append(out, f)
	}
	return out
}

// factSourceOf decides where one non-judge fact's value comes from. A
// declaration's own `[[outputs]]` entry says it either copies the resource
// observation (observed) or computes the value itself (derived); anything
// else a gate reads is written into the instance from outside (recorded).
func factSourceOf(def config.TaskDefinition, name string, observers map[string][]string) factSource {
	for _, out := range def.DynamicOutputs {
		for _, produced := range out.OutputNames() {
			if produced != name {
				continue
			}
			if out.FromResourceStatus {
				return observed
			}
			return derived
		}
	}
	if len(observers[name]) > 0 {
		return observed
	}
	return recorded
}

// loadShippedCatalogObservers indexes every state key the shipped observers
// publish by the observers that publish it.
func loadShippedCatalogObservers(t *testing.T) map[string][]string {
	t.Helper()
	cfg := shippedCatalogConfig(t)
	defs, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs(shipped catalog): %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("the shipped catalog declares no resource observer")
	}
	byKey := map[string][]string{}
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		keys, err := config.SchemaPropertyNames(defs[id].StateSchema, defs[id].ResolvedStateSchemaPath())
		if err != nil {
			t.Fatalf("observer %q state schema: %v", id, err)
		}
		for _, key := range keys {
			byKey[key] = append(byKey[key], id)
		}
	}
	return byKey
}

// pluginRootOf walks up from a declaration's own file to the plugin directory
// that mounted it, so a record lands beside the plugin that owns it.
func pluginRootOf(sourcePath string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(sourcePath)))
}
