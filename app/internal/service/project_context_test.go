package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// pcProjectWorkflow replaces the global review workflow whole, adding a node
// whose effect only the project declares.
const pcProjectWorkflow = `
[review]
kind     = "workflow"
resource = "issue"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[review.nodes]]
uses = "checkout"

[[review.nodes]]
uses = "agent"

[[review.nodes]]
id   = "extra"
uses = "planted"
`

const pcProjectEffect = `
[planted]
kind  = "effect"
scope = "session"

[planted.setup]
type   = "shell"
script = 'echo "{}"'

[planted.health.alive]
type = "noop"

[planted.terminal.attach]
type   = "shell"
script = 'echo attach'

[planted.terminal.capture]
type   = "shell"
script = 'touch "$HOME/captured"; echo captured'
`

// pcProject marks and trusts a project root declaring pcProjectWorkflow.
func pcProject(t *testing.T, f *wdFixture) string {
	t.Helper()
	root := filepath.Join(f.home, "projects", "widgets")
	wdWrite(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
	wdWrite(t, filepath.Join(root, ".plect", "tasks", "planted.toml"), pcProjectEffect)
	wdWrite(t, filepath.Join(root, ".plect", "workflows", "review.toml"), pcProjectWorkflow)
	f.cfg.TrustedProjectRoots = []string{root}
	return root
}

func pcSession(t *testing.T, store *state.Store, name string) *domain.Session {
	t.Helper()
	session, err := store.GetE(name)
	if err != nil || session == nil {
		t.Fatalf("session %q: %v, %v", name, session, err)
	}
	return session
}

func pcRevisionNodes(t *testing.T, session *domain.Session) map[string]bool {
	t.Helper()
	revision, ok, err := config.RevisionForSession(session)
	if err != nil || !ok {
		t.Fatalf("revision: %v, %v", ok, err)
	}
	nodes := map[string]bool{}
	for _, node := range revision.Workflow.Nodes {
		nodes[node.ID] = true
	}
	return nodes
}

func TestUp_RecordsTheSelectedContextAndCapturesItsDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project bool
	}{
		{"the global-only context", false},
		{"a selected project root", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWDFixture(t, wdWorkdirWorkflow)
			root := pcProject(t, f)
			params := UpParams{Identifier: wdIssueURL}
			want := ""
			if tc.project {
				params.ProjectRoot, want = root, root
			}

			if _, err := Up(f.cfg, f.store, params); err != nil {
				t.Fatalf("Up: %v", err)
			}
			session := pcSession(t, f.store, "issue-1")
			if session.ProjectRoot != want {
				t.Errorf("ProjectRoot = %q, want %q", session.ProjectRoot, want)
			}
			if got := pcRevisionNodes(t, session)["extra"]; got != tc.project {
				t.Errorf("captured the project's node = %v, want %v", got, tc.project)
			}
		})
	}
}

func TestWorkflowRebase_ReExpandsFromTheRecordedProjectRoot(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	root := pcProject(t, f)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, ProjectRoot: root}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	result, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: "issue-1", DryRun: true})
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if result.Class != RebaseCompatible {
		t.Errorf("class = %q (%v), want %q: the rebase must read the recorded root, not the global workflow", result.Class, result.Reasons, RebaseCompatible)
	}
}

func TestWorkflowRebase_RefusesARecordedRootThatIsNoLongerTrusted(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	root := pcProject(t, f)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, ProjectRoot: root}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	f.cfg.TrustedProjectRoots = nil

	_, err := WorkflowRebase(f.cfg, f.store, RebaseParams{Identifier: "issue-1", DryRun: true})
	var untrusted *config.UntrustedProjectError
	if !errors.As(err, &untrusted) || untrusted.Root != root {
		t.Fatalf("rebase = %v, want the revoked root refused rather than the global workflow selected", err)
	}
}

func TestTickSession_ChainSpawnInheritsTheTriggeringSessionsProjectRoot(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, "wf",
		[]taskFixture{workTaskWithChain(`
[[chains]]
id       = "review"
workflow = "reviewer"
[chains.when]
all = [ { judge_pending = "ac-met" } ]
`)},
		[]nodeFixture{{id: "work"}})
	root := filepath.Join(t.TempDir(), "widgets")
	wdWrite(t, filepath.Join(root, ".plect", "project.toml"), "schema_version = 2\n")
	wdWrite(t, filepath.Join(root, ".plect", "tasks", "planted.toml"), pcProjectEffect)
	wdWrite(t, filepath.Join(root, ".plect", "workflows", "reviewer.toml"), "[reviewer]\nkind = \"workflow\"\nresource = \"issue\"\n[[reviewer.nodes]]\nuses = \"planted\"\n")
	cfg.TrustedProjectRoots = []string{root}
	now := time.Now()
	work := &domain.Session{
		Name:        "review-1",
		Resource:    testResource("https://github.com/owner/repo/issues/1"),
		Workflow:    "wf",
		ProjectRoot: root,
		Nodes: map[string]*contract.TaskState{"work": {
			Scope:    contract.TaskScopeSession,
			TaskID:   "work",
			Status:   contract.TaskStatusProduced,
			Observed: &contract.ResourceObservation{State: map[string]any{"checks_status": "SUCCESS", "revision": "sha1"}, At: now},
		}},
		Tasks:     map[string]*contract.TaskState{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	pinSessionRevision(t, cfg, work)
	if err := store.Put(work); err != nil {
		t.Fatal(err)
	}

	res, err := TickSession(cfg, store, TickParams{SessionName: "review-1", SkipRefresh: true})
	if err != nil {
		t.Fatalf("TickSession: %v", err)
	}
	sp, _ := findSpawn(res.Chains, "review")
	if !sp.Spawned {
		t.Fatalf("chain spawn = %+v, want the project's workflow spawned", sp)
	}
	if got := pcSession(t, store, sp.TargetSession).ProjectRoot; got != root {
		t.Errorf("spawned ProjectRoot = %q, want the triggering session's %q", got, root)
	}
}

func TestLifecycle_RefusesASessionWhoseRecordedRootIsNoLongerUsable(t *testing.T) {
	for _, revoke := range []struct {
		name string
		do   func(t *testing.T, f *wdFixture, root string)
	}{
		{"root removed from trusted_project_roots", func(t *testing.T, f *wdFixture, root string) { f.cfg.TrustedProjectRoots = nil }},
		{"project marker deleted", func(t *testing.T, f *wdFixture, root string) {
			if err := os.Remove(filepath.Join(root, ".plect", "project.toml")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		for _, op := range []struct {
			name string
			run  func(f *wdFixture) error
		}{
			{"up", func(f *wdFixture) error {
				_, err := Up(f.cfg, f.store, UpParams{Identifier: "issue-1"})
				return err
			}},
			{"down", func(f *wdFixture) error {
				_, err := Down(f.cfg, f.store, DownParams{Identifier: "issue-1"})
				return err
			}},
			{"destroy", func(f *wdFixture) error {
				_, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: "issue-1", Force: true})
				return err
			}},
			{"task setup", func(f *wdFixture) error {
				_, err := TaskSetup(f.cfg, f.store, TaskSetupParams{TaskID: "planted", SessionName: "issue-1"})
				return err
			}},
			{"health", func(f *wdFixture) error {
				_, err := EvaluateHealth(f.cfg, f.store, "issue-1")
				return err
			}},
			{"attach", func(f *wdFixture) error {
				_, err := Attach(f.cfg, f.store, AttachParams{Identifier: "issue-1"})
				return err
			}},
			{"capture", func(f *wdFixture) error {
				_, err := Capture(f.cfg, f.store, CaptureParams{Identifier: "issue-1"})
				return err
			}},
		} {
			t.Run(revoke.name+"/"+op.name, func(t *testing.T) {
				f := newWDFixture(t, wdWorkdirWorkflow)
				root := pcProject(t, f)
				if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, ProjectRoot: root}); err != nil {
					t.Fatalf("Up: %v", err)
				}
				if op.name != "down" && op.name != "attach" && op.name != "capture" {
					if _, err := Down(f.cfg, f.store, DownParams{Identifier: "issue-1"}); err != nil {
						t.Fatalf("Down: %v", err)
					}
				}
				revoke.do(t, f, root)

				err := op.run(f)
				if err == nil || !strings.Contains(err.Error(), root) {
					t.Fatalf("%s = %v, want it refused naming the recorded root %s", op.name, err, root)
				}
				if _, statErr := os.Stat(filepath.Join(f.home, "captured")); statErr == nil {
					t.Errorf("%s ran the project's terminal.capture script", op.name)
				}
			})
		}
	}
}
