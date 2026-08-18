package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// taskFixture is a terse spec for one task definition. Scope defaults to
// "run". Empty fields are omitted from the generated TOML. extra is raw TOML
// appended verbatim (e.g. an [outputs_schema] block).
//
// attach/capture/sendText/sendKeys build a [terminal] table when any one is
// set — the config package's all-or-nothing rule requires all four together,
// so the writer below fills any of the four a test left unset with a trivial
// always-succeeds stub. Most fixtures only care about one or two verbs; the
// stubs keep the other verbs inert instead of forcing every attach/capture
// test to also spell out send_text/send_keys it never exercises.
type taskFixture struct {
	id             string
	scope          string
	setup          string
	cleanup        string
	healthcheck    string
	movementSignal string
	attach         string
	capture        string
	sendText       string
	sendKeys       string
	extra          string
}

// stubTerminalVerb fills an unset [terminal] verb with a trivial
// always-succeeds command, so a fixture that only cares about (say) attach
// can leave capture/send_text/send_keys unset without tripping the
// all-or-nothing [terminal] validation.
func stubTerminalVerb(v string) string {
	if v == "" {
		return "true"
	}
	return v
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
func writeWorkflowFixture(t *testing.T, workdirsRoot, wfID string, defs []taskFixture, nodes []nodeFixture) *config.Config {
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
		if d.extra != "" {
			b.WriteString(d.extra)
			b.WriteString("\n")
		}
		// [terminal] goes last: TOML scopes every bare key = value after a
		// table header to that table, so anything else in this file must
		// come before it (extra may itself open another table like
		// [outputs_schema], which is fine — a new table header ends
		// [terminal]'s scope, but nothing here reopens a bare key after it).
		if d.attach != "" || d.capture != "" || d.sendText != "" || d.sendKeys != "" {
			fmt.Fprintf(&b, "\n[terminal]\nattach = %q\ncapture = %q\nsend_text = %q\nsend_keys = %q\n",
				stubTerminalVerb(d.attach), stubTerminalVerb(d.capture), stubTerminalVerb(d.sendText), stubTerminalVerb(d.sendKeys))
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
		WorkspaceDirsRoot: workdirsRoot,
		BaseDir:           baseDir,
	}
}
