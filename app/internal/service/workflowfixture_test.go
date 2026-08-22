package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// taskFixture is a terse spec for one task definition. Scope defaults to
// "run". Empty fields are omitted from the generated TOML. extra is raw TOML
// appended verbatim (e.g. an [outputs_schema] block).
//
// attach/capture/sendText/sendKeys build a `[terminal]` table. A verb is
// available on its own, but the writer below still fills any of the four a
// test left unset with a trivial always-succeeds stub, so a case consuming
// one it did not name resolves rather than failing. Most fixtures only care
// about one or two verbs; the
// stubs keep the other verbs inert instead of forcing every attach/capture
// test to also spell out send_text/send_keys it never exercises.
type taskFixture struct {
	id       string
	scope    string
	setup    string
	cleanup  string
	alive    string
	activity string
	attach   string
	capture  string
	sendText string
	sendKeys string
	extra    string
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

// effectFixtureDoc renders one effect declaration: the definition table, its
// lifecycle actions and probes as shell actions, its terminal verbs, and
// whatever else the case declares under that table.
func effectFixtureDoc(d taskFixture) string {
	bare, tables := splitFixtureExtra(d.id, d.extra)
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\nkind = \"effect\"\n", d.id)
	if d.scope != "" {
		fmt.Fprintf(&b, "scope = %q\n", d.scope)
	}
	b.WriteString(bare)
	actions := []struct {
		path   string
		script string
	}{
		{"setup", d.setup},
		{"cleanup", d.cleanup},
		{"health.alive", d.alive},
		{"health.activity", d.activity},
	}
	if d.attach != "" || d.capture != "" || d.sendText != "" || d.sendKeys != "" {
		actions = append(actions,
			struct {
				path   string
				script string
			}{"terminal.attach", stubTerminalVerb(d.attach)},
			struct {
				path   string
				script string
			}{"terminal.capture", stubTerminalVerb(d.capture)},
			struct {
				path   string
				script string
			}{"terminal.send_text", stubTerminalVerb(d.sendText)},
			struct {
				path   string
				script string
			}{"terminal.send_keys", stubTerminalVerb(d.sendKeys)},
		)
	}
	for _, action := range actions {
		if action.script == "" {
			continue
		}
		b.WriteString(shellFixtureAction(d.id, action.path, action.script))
	}
	b.WriteString(tables)
	return b.String()
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
		if err := os.WriteFile(filepath.Join(tasksDir, d.id+".toml"), []byte(effectFixtureDoc(d)), 0o644); err != nil {
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

// legacyProjection matches the projections a fixture script writes inline.
// A fixture states its script the way an author does — a shell variable per
// value — but writing the bind table out per case would bury what each case
// is actually about, so these helpers derive it: one binding per distinct
// projection, named after the key it reads.
var legacyProjection = regexp.MustCompile(
	`\{\{\s*(?:get\s+)?\.(Self|Prev|Inputs|Locals|SessionInputs)\s+"([A-Za-z0-9_]+)"(?:\s+"[^"]*")?\s*\}\}` +
		`|\{\{\s*\.(Self|Prev|Inputs|Locals|SessionInputs)\.([A-Za-z0-9_]+)\s*\}\}` +
		`|\{\{\s*\.Nodes\.([A-Za-z0-9_]+)\.outputs\.([A-Za-z0-9_]+)\s*\}\}` +
		`|\{\{\s*\.Workflow\.outputs\.([A-Za-z0-9_]+)\s*\}\}` +
		`|\{\{\s*\.(SessionName|ResourceID|WorkspaceDirPath|Branch|ParentSession)\s*\}\}` +
		`|\{\{\s*(?:terminal|bin)\s+"([A-Za-z0-9_-]+)"[^}]*\}\}`)

var singleQuotedBinding = regexp.MustCompile(`'(\$\{[A-Za-z0-9_]+-\})'`)

var legacyRootPaths = map[string]string{
	"Self":          "self.outputs.",
	"Prev":          "prev.",
	"Inputs":        "inputs.",
	"Locals":        "locals.",
	"SessionInputs": "session.inputs.",
}

var legacyScalarPaths = map[string]struct{ name, path string }{
	"SessionName":      {"session_name", "session.name"},
	"ResourceID":       {"resource_id", "resource.id"},
	"WorkspaceDirPath": {"workspace_dir", "workspace.dir"},
	"Branch":           {"branch", "workspace.branch"},
	"ParentSession":    {"parent_session", "session.parent"},
}

// shellFixtureAction renders one shell action from a fixture's script,
// binding every value the script reads.
func shellFixtureAction(id, path, script string) string {
	binds := map[string]string{}
	rendered := legacyProjection.ReplaceAllStringFunc(script, func(match string) string {
		name, value := fixtureBinding(match)
		binds[name] = value
		return "${" + name + "-}"
	})
	// A fixture wrote its projection inside single quotes, where a shell
	// expands nothing; the binding it became has to be expanded, so the
	// quoting around it changes with it.
	rendered = singleQuotedBinding.ReplaceAllString(rendered, `"$1"`)
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s.%s]\ntype = \"shell\"\nscript = %q\n", id, path, rendered)
	if len(binds) > 0 {
		fmt.Fprintf(&b, "\n[%s.%s.bind]\n", id, path)
		names := make([]string, 0, len(binds))
		for name := range binds {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "%s = %s\n", name, binds[name])
		}
	}
	return b.String()
}

// fixtureBinding names the binding one projection becomes, and the value it
// resolves. Every binding is optional: a fixture script is shell-defensive
// about what a partial setup left behind, the same way a real one is.
func fixtureBinding(match string) (name, value string) {
	groups := legacyProjection.FindStringSubmatch(match)
	switch {
	case groups[1] != "":
		return groups[2], fmt.Sprintf("{ from = %q, optional = true }", legacyRootPaths[groups[1]]+groups[2])
	case groups[3] != "":
		return groups[4], fmt.Sprintf("{ from = %q, optional = true }", legacyRootPaths[groups[3]]+groups[4])
	case groups[5] != "":
		return groups[5] + "_" + groups[6], fmt.Sprintf("{ from = %q, optional = true }", "nodes."+groups[5]+".outputs."+groups[6])
	case groups[7] != "":
		return "wf_" + groups[7], fmt.Sprintf("{ from = %q, optional = true }", "workflow.outputs."+groups[7])
	case groups[8] != "":
		scalar := legacyScalarPaths[groups[8]]
		return scalar.name, fmt.Sprintf("{ from = %q, optional = true }", scalar.path)
	default:
		if strings.Contains(match, "terminal") {
			return "terminal_" + groups[9], fmt.Sprintf("{ terminal = %q }", groups[9])
		}
		return "bin_" + strings.ReplaceAll(groups[9], "-", "_"), fmt.Sprintf("{ bin = %q }", groups[9])
	}
}
