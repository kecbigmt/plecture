package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// A completion predicate is only as sound as its wiring to the facts it reads.
// Every fact a shipped gate reads has exactly one source — a key the declared
// observer publishes about the resource, a key something records into the
// instance, or a judge's verdict — and now that the completion surface reads
// its own two roots, the declaration names which one out loud.
//
// This records that mapping per plugin, from the shipped declarations, and
// asserts the part of it that can be wrong on its own: an observed fact the
// declared observer does not publish, or a recorded fact the document's own
// state_schema does not declare, is a gate that can never satisfy. (Load-time
// validation rejects both; the record is what makes the wiring reviewable.)
//
// An expression leaf contributes its operands rather than one opaque line:
// that is what moving this surface bought — a derived comparison's operands
// are declarations now, not text inside a script.
//
// Set PLECT_UPDATE_GATE_RECORDS=1 to rewrite the records after an intended
// change.

const gateRecordName = "gate-keys.txt"

// factSource is where one gate fact's value comes from.
type factSource string

const (
	// observed names a key the declared resource observer publishes.
	observed factSource = "observed"
	// recorded names a key something outside this instance writes into it.
	recorded factSource = "recorded"
	// judged names a reviewer's verdict against a judge leaf.
	judged factSource = "judged"
)

type gateFact struct {
	Name   string
	Source factSource
	// Declarer is the contract that says this fact exists: the observer for
	// an observed key, the document itself for a recorded one.
	Declarer string
	// Where says which predicate reads it: the completion predicate, a
	// chain's trigger, or both.
	Where string
}

func TestShippedGates_KeyMappingMatchesTheRecord(t *testing.T) {
	cfg := shippedCatalogConfig(t)
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments(shipped catalog): %v", err)
	}
	observers := loadShippedCatalogObservers(t)

	byPlugin := map[string][]string{}
	for id, doc := range docs {
		if doc.DoneWhen == nil && len(doc.Chains) == 0 {
			continue
		}
		byPlugin[pluginRootOf(doc.SourcePath)] = append(byPlugin[pluginRootOf(doc.SourcePath)], id)
	}
	if len(byPlugin) == 0 {
		t.Fatal("no shipped declaration carries a gate; this test is checking nothing")
	}

	for root, ids := range byPlugin {
		sort.Strings(ids)
		t.Run(filepath.Base(root), func(t *testing.T) {
			var b strings.Builder
			for _, id := range ids {
				doc := docs[id]
				facts := gateFactsOf(doc, observers)
				fmt.Fprintf(&b, "== %s (%s)\n", id, doc.ResourceObserver)
				for _, f := range facts {
					if f.Declarer == "" {
						t.Errorf("%s: gate reads %q, which no contract declares", id, f.Name)
					}
					line := fmt.Sprintf("%-42s %-9s %s", f.Name, f.Source, f.Where)
					if f.Declarer != "" {
						line += "   " + f.Declarer
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

// gateFactsOf classifies every fact one document's gates read, in a stable
// order.
func gateFactsOf(doc config.TaskDocument, observers map[string][]string) []gateFact {
	where := map[string]map[string]bool{}
	note := func(name, predicate string) {
		if where[name] == nil {
			where[name] = map[string]bool{}
		}
		where[name][predicate] = true
	}
	sources := map[string]factSource{}
	read := func(path, predicate string) {
		sources[path] = observed
		if strings.HasPrefix(path, "self.state.") {
			sources[path] = recorded
		}
		note(path, predicate)
	}
	if doc.DoneWhen != nil {
		for _, leaf := range doc.DoneWhen.All {
			switch {
			case leaf.Judge != "":
				sources[leaf.ID] = judged
				note(leaf.ID, "done_when")
			case leaf.IsExpr():
				for _, path := range completionPathsIn(leaf.Expr) {
					read(path, "done_when")
				}
			default:
				read(leaf.Check, "done_when")
			}
		}
	}
	for _, ch := range doc.Chains {
		for _, fact := range ch.When.All {
			switch {
			case fact.JudgePending != "":
				sources[fact.JudgePending] = judged
				note(fact.JudgePending, "chain."+ch.ID)
			case fact.JudgeAction != "":
				sources[fact.JudgeAction] = judged
				note(fact.JudgeAction, "chain."+ch.ID)
			case fact.Expr != "":
				for _, path := range completionPathsIn(fact.Expr) {
					read(path, "chain."+ch.ID)
				}
			default:
				read(fact.Check, "chain."+ch.ID)
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
		f.Declarer = declarerOf(f, doc, observers)
		out = append(out, f)
	}
	return out
}

// declarerOf names the contract a fact resolves against, and is empty when
// none declares it — the one thing this record can catch on its own.
func declarerOf(f gateFact, doc config.TaskDocument, observers map[string][]string) string {
	switch f.Source {
	case judged:
		return doc.ID
	case recorded:
		key := strings.TrimPrefix(f.Name, "self.state.")
		declared, err := config.SchemaPropertyNames(doc.StateSchema, doc.ResolvedStateSchemaPath())
		if err != nil {
			return ""
		}
		for _, name := range declared {
			if name == key {
				return doc.ID
			}
		}
		return ""
	}
	key := strings.TrimPrefix(f.Name, "resource.state.")
	for _, id := range observers[key] {
		if id == doc.ResourceObserver {
			return id
		}
	}
	return ""
}

// completionPathsIn lists the rooted paths one expression leaf reads, so a
// derived comparison records as its operands rather than as one opaque line.
func completionPathsIn(expr string) []string {
	paths := lang.CompletionExpressionPaths(expr)
	sort.Strings(paths)
	return paths
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
