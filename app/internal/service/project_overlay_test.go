package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

const poOverlayEffect = `
[planted]
kind  = "effect"
scope = "session"

[planted.setup]
type   = "shell"
script = 'echo "{}"'

[planted.health.alive]
type = "noop"
`

const poOverlayWorkflow = `
[review]
kind = "workflow"

[[review.nodes]]
id   = "extra"
uses = "planted"
`

func poRevisionUses(t *testing.T, f *wdFixture) (effects map[string]bool, nodes map[string]bool) {
	t.Helper()
	session, err := f.store.GetE("issue-1+review")
	if err != nil || session == nil {
		t.Fatalf("session: %v, %v", session, err)
	}
	revision, ok, err := config.RevisionForSession(session)
	if err != nil || !ok {
		t.Fatalf("revision: %v, %v", ok, err)
	}
	effects, nodes = map[string]bool{}, map[string]bool{}
	for key := range revision.Effects {
		effects[key] = true
	}
	for _, node := range revision.Workflow.Nodes {
		nodes[node.ID] = true
	}
	return effects, nodes
}

func poSubdirectoryOf(t *testing.T, root string) string {
	t.Helper()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestUp_AncestorOverlayIsCapturedOnlyWhenItsDirectoryIsTrusted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted bool
	}{
		{"an untrusted checkout root contributes neither effect nor node", false},
		{"a trusted root contributes both", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWDFixture(t, wdWorkdirWorkflow)
			checkout := filepath.Join(f.home, "clone")
			wdWrite(t, filepath.Join(checkout, ".plect", "tasks", "planted.toml"), poOverlayEffect)
			wdWrite(t, filepath.Join(checkout, ".plect", "workflows", "review.toml"), poOverlayWorkflow)
			if tc.trusted {
				f.cfg.TrustedProjectRoots = []string{checkout}
			}

			if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, InvocationDir: poSubdirectoryOf(t, checkout)}); err != nil {
				t.Fatalf("Up: %v", err)
			}
			effects, nodes := poRevisionUses(t, f)
			if effects["planted"] != tc.trusted || nodes["extra"] != tc.trusted {
				t.Errorf("captured effect=%v node=%v, want both %v", effects["planted"], nodes["extra"], tc.trusted)
			}
		})
	}
}

func TestWorkflowRebase_ReExpandsFromTheContextTheSessionWasCreatedIn(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	project := filepath.Join(f.home, "projects")
	wdWrite(t, filepath.Join(project, ".plect", "tasks", "planted.toml"), poOverlayEffect)
	wdWrite(t, filepath.Join(project, ".plect", "workflows", "review.toml"), poOverlayWorkflow)
	f.cfg.TrustedProjectRoots = []string{project}
	created := filepath.Join(project, "checkout-a")
	if err := os.MkdirAll(created, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, InvocationDir: created}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	result, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: "issue-1+review", DryRun: true})
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if result.Class != RebaseCompatible {
		t.Errorf("class = %q (%v), want %q: the overlay node was dropped because the caller's directory selected other overlays", result.Class, result.Reasons, RebaseCompatible)
	}
}
