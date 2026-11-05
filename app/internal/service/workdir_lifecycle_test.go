package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const wdIssueURL = "https://forge.example/issues/1"

// wdFixture is a global config with one resource, a directory-creating
// session-scoped effect and an agent effect that records where it ran, plus a
// scratch home so a preparation node's directory is observable.
type wdFixture struct {
	home  string
	base  string
	store *state.Store
	cfg   *config.Config
}

func wdWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newWDFixture(t *testing.T, workflowBody string) *wdFixture {
	t.Helper()
	home := realPath(t, t.TempDir())
	t.Setenv("HOME", home)
	base := t.TempDir()
	wdWrite(t, filepath.Join(base, "resources", "issue.toml"), `
[issue]
kind  = "resource"
match = '^https://forge\.example/issues/(?P<number>\d+)$'
name  = { expr = "'issue-' + match.number" }

[issue.observe]
type    = "exec"
command = "true"
`)
	wdWrite(t, filepath.Join(base, "tasks", "checkout.toml"), `
[checkout]
kind  = "effect"
scope = "session"

[checkout.setup]
type   = "shell"
script = '''
dir="$HOME/checkouts/$name"
mkdir -p "$dir"
printf '{"workspace_dir":"%s","cwd":"%s"}' "$dir" "$(pwd -P)"
'''

[checkout.setup.bind]
name = { from = "session.name" }

[checkout.cleanup]
type   = "shell"
script = '''
printf '%s %s\n' "$force" "$delete_branch" > "$HOME/cleanup-intent"
rm -rf "$dir"
'''

[checkout.cleanup.bind]
dir           = { from = "self.outputs.workspace_dir" }
force         = { from = "force" }
delete_branch = { from = "cleanup.inputs.delete_branch", default = "unset" }

[checkout.cleanup.inputs_schema]
type = "object"

[checkout.cleanup.inputs_schema.properties]
delete_branch = { type = "boolean" }

[checkout.health.alive]
type = "noop"
`)
	wdWrite(t, filepath.Join(base, "tasks", "agent.toml"), `
[agent]
kind  = "effect"
scope = "session"

[agent.setup]
type   = "shell"
script = '''
printf '{"cwd":"%s"}' "$(pwd -P)"
'''

[agent.cleanup]
type   = "shell"
script = 'true'

[agent.health.alive]
type   = "shell"
script = 'true'
`)
	wdWrite(t, filepath.Join(base, "workflows", "review.toml"), workflowBody)
	return &wdFixture{home: home, base: base, store: state.NewStore(t.TempDir()), cfg: &config.Config{BaseDir: base}}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

const wdWorkdirWorkflow = `
[review]
kind     = "workflow"
resource = "issue"
workdir  = { from = "nodes.checkout.outputs.workspace_dir" }

[[review.nodes]]
uses = "checkout"

[[review.nodes]]
uses = "agent"
`

func wdSession(t *testing.T, f *wdFixture) (string, map[string]*contract.TaskState) {
	t.Helper()
	name := "issue-1+review"
	session, err := f.store.GetE(name)
	if err != nil || session == nil {
		t.Fatalf("session %q: %v, %v", name, session, err)
	}
	return name, session.Nodes
}

func wdCwd(t *testing.T, nodes map[string]*contract.TaskState, node string) string {
	t.Helper()
	st := nodes[node]
	if st == nil {
		t.Fatalf("node %q has no record", node)
	}
	cwd, _ := st.Outputs["cwd"].(string)
	return cwd
}

func TestUp_PreparationNodesRunInHomeAndOtherNodesInTheWorkdir_OnEveryRoute(t *testing.T) {
	callerDir := realPath(t, t.TempDir())
	routes := []struct {
		name   string
		mutate func(*UpParams)
	}{
		{"direct invocation", func(p *UpParams) { p.InvocationDir = callerDir }},
		{"chain or population, which have no caller directory", func(p *UpParams) {}},
		{"population-owned session", func(p *UpParams) {
			p.Population = &contract.PopulationProvenance{Workflow: "review", Name: "standing"}
		}},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			f := newWDFixture(t, wdWorkdirWorkflow)
			t.Chdir(callerDir)
			params := UpParams{Identifier: wdIssueURL, ParentSession: ""}
			route.mutate(&params)
			if params.Population != nil {
				if err := f.store.UpdatePopulation("review/standing", func(*state.PopulationState) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Up(f.cfg, f.store, params); err != nil {
				t.Fatalf("Up: %v", err)
			}
			_, nodes := wdSession(t, f)

			workdir, _ := nodes["checkout"].Outputs["workspace_dir"].(string)
			if workdir == "" {
				t.Fatalf("checkout produced no workspace_dir: %+v", nodes["checkout"])
			}
			if got := wdCwd(t, nodes, "checkout"); got != f.home {
				t.Errorf("the preparation node ran in %q, want the user's home %q", got, f.home)
			}
			if nodes["checkout"].Directory != f.home {
				t.Errorf("recorded preparation directory = %q, want %q", nodes["checkout"].Directory, f.home)
			}
			if got := wdCwd(t, nodes, "agent"); got != realPath(t, workdir) {
				t.Errorf("the agent ran in %q, want the workdir %q", got, workdir)
			}
			if nodes["agent"].Directory != workdir {
				t.Errorf("recorded agent directory = %q, want %q", nodes["agent"].Directory, workdir)
			}
		})
	}
}

func TestUp_WithoutWorkdir_NoNodeHasADefaultDirectory(t *testing.T) {
	f := newWDFixture(t, `
[review]
kind     = "workflow"
resource = "issue"

[[review.nodes]]
uses = "agent"
`)
	processDir := realPath(t, t.TempDir())
	t.Chdir(processDir)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	_, nodes := wdSession(t, f)
	if nodes["agent"].Directory != "" {
		t.Errorf("recorded directory = %q, want none", nodes["agent"].Directory)
	}
	if got := wdCwd(t, nodes, "agent"); got != processDir {
		t.Errorf("agent ran in %q, want the process's own directory %q (no default directory is given)", got, processDir)
	}
}

func TestUp_VanishedWorkdirInvalidatesTheNodeAndKeepsItsReleaseObligation(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	name, nodes := wdSession(t, f)
	workdir, _ := nodes["checkout"].Outputs["workspace_dir"].(string)
	if err := os.RemoveAll(workdir); err != nil {
		t.Fatal(err)
	}

	if _, err := Up(f.cfg, f.store, UpParams{Identifier: name}); err == nil {
		t.Fatal("a second up with the workdir gone succeeded")
	}
	_, nodes = wdSession(t, f)
	agent := nodes["agent"]
	if agent.Status != contract.TaskStatusFailed || agent.Directory != workdir || agent.Error == "" {
		t.Errorf("agent = status %q, directory %q, error %q; want a failed record that still names %q", agent.Status, agent.Directory, agent.Error, workdir)
	}
	if _, err := os.Stat(workdir); err == nil {
		t.Errorf("the vanished workdir was recreated")
	}

	if _, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name}); err == nil {
		t.Fatal("destroy without --force released a node whose cleanup directory is gone")
	}
	if session, _ := f.store.GetE(name); session == nil || session.Nodes["agent"] == nil {
		t.Errorf("the failed record did not survive the refused destroy")
	}
	if _, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name, Force: true}); err != nil {
		t.Fatalf("destroy --force: %v", err)
	}
}

func TestDestroy_CleanupInputsReachOnlyTheEffectsThatDeclareThem(t *testing.T) {
	t.Run("a declared input and force are handed to cleanup", func(t *testing.T) {
		f := newWDFixture(t, wdWorkdirWorkflow)
		if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
			t.Fatalf("Up: %v", err)
		}
		name, _ := wdSession(t, f)
		if _, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name, Force: true, CleanupInputs: map[string]string{"delete_branch": "true"}}); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(f.home, "cleanup-intent"))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(raw)); got != "true true" {
			t.Errorf("cleanup saw force and delete_branch = %q, want %q", got, "true true")
		}
	})
	t.Run("an input no released effect declares is refused before anything is released", func(t *testing.T) {
		f := newWDFixture(t, wdWorkdirWorkflow)
		if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
			t.Fatalf("Up: %v", err)
		}
		name, nodes := wdSession(t, f)
		workdir, _ := nodes["checkout"].Outputs["workspace_dir"].(string)
		_, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name, CleanupInputs: map[string]string{"delete_brnach": "true"}})
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != ErrInvalidInput {
			t.Fatalf("Destroy error = %v, want an invalid-input error naming the undeclared key", err)
		}
		if _, statErr := os.Stat(workdir); statErr != nil {
			t.Errorf("the workdir was released although the destroy was refused: %v", statErr)
		}
	})
	t.Run("an input with the wrong type is refused", func(t *testing.T) {
		f := newWDFixture(t, wdWorkdirWorkflow)
		if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL}); err != nil {
			t.Fatalf("Up: %v", err)
		}
		name, _ := wdSession(t, f)
		_, err := Destroy(f.cfg, f.store, DestroyParams{Identifier: name, CleanupInputs: map[string]string{"delete_branch": "maybe"}})
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != ErrInvalidInput {
			t.Fatalf("Destroy error = %v, want an invalid-input error for a non-boolean delete_branch", err)
		}
	})
}

func TestUp_ResourceTypeSelectsTheWorkflow(t *testing.T) {
	f := newWDFixture(t, wdWorkdirWorkflow)
	wdWrite(t, filepath.Join(f.base, "workflows", "other.toml"), `
[other]
kind     = "workflow"
resource = "issue"

[[other.nodes]]
uses = "agent"
`)
	t.Run("two workflows accepting the type is an error that names both", func(t *testing.T) {
		_, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL})
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != ErrInvalidInput || !strings.Contains(svcErr.Message, "review") || !strings.Contains(svcErr.Message, "other") {
			t.Fatalf("Up error = %v, want an ambiguity naming both candidate workflows", err)
		}
	})
	t.Run("--workflow chooses among them", func(t *testing.T) {
		if _, err := Up(f.cfg, f.store, UpParams{Identifier: wdIssueURL, Workflow: "other"}); err != nil {
			t.Fatalf("Up: %v", err)
		}
		if session, _ := f.store.GetE("issue-1+other"); session == nil {
			t.Fatal("no session named after the resource instance and the workflow")
		}
	})
	t.Run("--workflow of another type is an error", func(t *testing.T) {
		wdWrite(t, filepath.Join(f.base, "resources", "pull.toml"), `
[pull]
kind  = "resource"
match = '^https://forge\.example/pulls/(?P<number>\d+)$'
name  = { expr = "'pull-' + match.number" }

[pull.observe]
type    = "exec"
command = "true"
`)
		_, err := Up(f.cfg, f.store, UpParams{Identifier: "https://forge.example/pulls/2", Workflow: "review"})
		var svcErr *Error
		if !errors.As(err, &svcErr) || svcErr.Code != ErrInvalidInput {
			t.Fatalf("Up error = %v, want an error: review accepts issues, not pulls", err)
		}
	})
	t.Run("an identifier no resource recognizes creates nothing", func(t *testing.T) {
		_, err := Up(f.cfg, f.store, UpParams{Identifier: "https://elsewhere.example/x", Workflow: "review"})
		if err == nil {
			t.Fatal("Up accepted an identifier no resource recognizes")
		}
	})
}
