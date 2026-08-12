package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
)

// taskFixture is a terse spec for one task definition. Scope defaults to
// "run". Empty fields are omitted from the generated TOML. extra is raw TOML
// appended verbatim (e.g. an [outputs_schema] block).
type taskFixture struct {
	id             string
	scope          string
	setup          string
	cleanup        string
	healthcheck    string
	movementSignal string
	attach         string
	capture        string
	primary        bool
	execution      string
	extra          string
}

// nodeFixture mirrors WorkflowNode for fixture authoring. ID defaults to Uses;
// Uses defaults to ID when only one is given.
type nodeFixture struct {
	id     string
	uses   string
	inputs map[string]string
}

// writeWorkflowFixture writes a workflow file + task definitions under a
// temp config dir, returns the matching *config.Config with BaseDir set so
// cfg.LoadWorkflows / LoadTaskDefinitions pick them up.
//
// The fixture sits at <BaseDir>/workflows/<wfID>.toml plus
// <BaseDir>/tasks/<id>.toml. Service tests that used to declare tasks on
// cfg.Tasks now declare them here and freeze `wfID` onto their session.
func writeWorkflowFixture(t *testing.T, worktreesRoot, wfID string, defs []taskFixture, nodes []nodeFixture) *config.Config {
	t.Helper()
	baseDir := t.TempDir()
	tasksDir := filepath.Join(baseDir, "tasks")
	workflowsDir := filepath.Join(baseDir, "workflows")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workflowsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		var b strings.Builder
		if d.scope != "" {
			fmt.Fprintf(&b, "scope = %q\n", d.scope)
		}
		if d.setup != "" {
			fmt.Fprintf(&b, "setup = %q\n", d.setup)
		}
		if d.cleanup != "" {
			fmt.Fprintf(&b, "cleanup = %q\n", d.cleanup)
		}
		if d.healthcheck != "" {
			fmt.Fprintf(&b, "healthcheck = %q\n", d.healthcheck)
		}
		if d.movementSignal != "" {
			fmt.Fprintf(&b, "movement_signal = %q\n", d.movementSignal)
		}
		if d.attach != "" {
			fmt.Fprintf(&b, "attach = %q\n", d.attach)
		}
		if d.capture != "" {
			fmt.Fprintf(&b, "capture = %q\n", d.capture)
		}
		if d.primary {
			b.WriteString("primary = true\n")
		}
		if d.execution != "" {
			fmt.Fprintf(&b, "execution = %q\n", d.execution)
		}
		if d.extra != "" {
			b.WriteString(d.extra)
			b.WriteString("\n")
		}
		if err := os.WriteFile(filepath.Join(tasksDir, d.id+".toml"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var w strings.Builder
	for _, n := range nodes {
		w.WriteString("[[nodes]]\n")
		if n.id != "" {
			fmt.Fprintf(&w, "id = %q\n", n.id)
		}
		if n.uses != "" {
			fmt.Fprintf(&w, "uses = %q\n", n.uses)
		}
		if len(n.inputs) > 0 {
			w.WriteString("[nodes.inputs]\n")
			for k, v := range n.inputs {
				fmt.Fprintf(&w, "%s = %q\n", k, v)
			}
		}
		w.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(workflowsDir, wfID+".toml"), []byte(w.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		WorktreesRoot: worktreesRoot,
		BaseDir:       baseDir,
	}
}
