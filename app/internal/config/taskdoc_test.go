package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// The container loads end to end: frontmatter declares the task, the body is
// its instruction, and the contract pass resolves every completion key
// against the two schemas that declare them.
func TestLoadTaskDocuments_LoadsAndValidates(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), `
[issue_pr]
kind  = "resource_observer"
match = '^https://github\.com/'

[issue_pr.observe]
type    = "exec"
command = "true"

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
resource_kind = { type = "string" }
revision      = { type = "string" }
`)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "Review a resource and record a verdict"
resource_observer = "issue_pr"

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
]
+++
Review {{ resource.id }} and record a verdict against its current revision.
`)
	cfg := &Config{BaseDir: base}
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments: %v", err)
	}
	doc, ok := docs["review"]
	if !ok {
		t.Fatalf("review not loaded: %+v", docs)
	}
	if doc.ResourceObserver != "issue_pr" {
		t.Errorf("ResourceObserver = %q", doc.ResourceObserver)
	}
	if !strings.HasPrefix(doc.Instruction, "Review {{ resource.id }}") {
		t.Errorf("Instruction = %q, want the body below the frontmatter", doc.Instruction)
	}
	if doc.DoneWhen == nil || len(doc.DoneWhen.All) != 2 {
		t.Fatalf("done_when = %+v, want two leaves", doc.DoneWhen)
	}
	if !doc.DoneWhen.All[1].IsExpr() {
		t.Errorf("second leaf = %+v, want the expression leaf", doc.DoneWhen.All[1])
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs: %v", err)
	}
	if err := cfg.ValidateTaskDocuments(docs, observers, nil); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}
}

// A completion key the declared observer does not publish is a load error:
// the observer's contract is what says the key exists.
func TestValidateTaskDocuments_UnpublishedKeyRejected(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), `
[issue_pr]
kind  = "resource_observer"
match = '^https://github\.com/'

[issue_pr.observe]
type    = "exec"
command = "true"

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
resource_kind = { type = "string" }
`)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "Reads a key its observer never publishes"
resource_observer = "issue_pr"

[review.done_when]
all = [{ check = "resource.state.nope", eq = "yes" }]
+++
Review it.
`)
	cfg := &Config{BaseDir: base}
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments: %v", err)
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs: %v", err)
	}
	err = cfg.ValidateTaskDocuments(docs, observers, nil)
	if err == nil {
		t.Fatal("expected a load error for an unpublished completion key")
	}
	if !strings.Contains(err.Error(), "resource.state.nope") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// A self.state key the document does not declare is the same load error from
// the other side: this document's own state_schema is what says it keeps it.
func TestValidateTaskDocuments_UndeclaredSelfStateKeyRejected(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "Reads state it never declared"
resource_observer = "issue_pr"

[review.done_when]
all = [{ expr = "self.state.verdict_revision == resource.state.revision" }]
+++
Review it.
`)
	cfg := &Config{BaseDir: base}
	docs, observers := loadDocsAndObservers(t, cfg)
	err := cfg.ValidateTaskDocuments(docs, observers, nil)
	if err == nil {
		t.Fatal("expected a load error for an undeclared self.state key")
	}
	if !strings.Contains(err.Error(), "verdict_revision") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

func TestValidateTaskDocuments_UnknownObserverRejected(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "Written for an observer nothing declares"
resource_observer = "no_such_observer"

[review.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
+++
Review it.
`)
	cfg := &Config{BaseDir: base}
	docs, observers := loadDocsAndObservers(t, cfg)
	err := cfg.ValidateTaskDocuments(docs, observers, nil)
	if err == nil {
		t.Fatal("expected a load error for an unresolvable resource_observer")
	}
	if !strings.Contains(err.Error(), "no_such_observer") {
		t.Errorf("error = %v, want it to name the reference", err)
	}
}

func TestLoadTaskDocuments_RejectsNonTaskKind(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "tasks", "not-a-task.md"), `+++
[not_a_task]
kind        = "effect"
description = "A Markdown document declaring a bodiless kind"

[not_a_task.setup]
type    = "shell"
script  = "true"
+++
A body a kind without one cannot carry.
`)
	cfg := &Config{BaseDir: base}
	_, err := cfg.LoadTaskDocuments("")
	if err == nil {
		t.Fatal("expected a load error for a Markdown document declaring a bodiless kind")
	}
	if !strings.Contains(err.Error(), "effect") {
		t.Errorf("error = %v, want it to name the declared kind", err)
	}
}

func TestLoadTaskDocuments_RejectsDuplicateIDInOneLayer(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "tasks", "a.md"), duplicateTaskDoc)
	writeFile(t, filepath.Join(base, "tasks", "b.md"), duplicateTaskDoc)
	cfg := &Config{BaseDir: base}
	_, err := cfg.LoadTaskDocuments("")
	if err == nil {
		t.Fatal("expected a load error for two documents declaring one id in the same layer")
	}
	if !strings.Contains(err.Error(), "review") {
		t.Errorf("error = %v, want it to name the id", err)
	}
}

// Clone content must not declare the work it is about: a task document found
// under the workspace directory is rejected rather than quietly ignored.
func TestLoadTaskDocuments_RejectsWorkspaceDirLayer(t *testing.T) {
	base := t.TempDir()
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, ".plect", "tasks", "review.md"), duplicateTaskDoc)
	cfg := &Config{BaseDir: base}
	_, err := cfg.LoadTaskDocuments(workspace)
	if err == nil {
		t.Fatal("expected a load error for a task document inside the workspace directory")
	}
	if !strings.Contains(err.Error(), "workspace directory") {
		t.Errorf("error = %v, want it to say where the document was found", err)
	}
}

const minimalObserver = `
[issue_pr]
kind  = "resource_observer"
match = '^https://github\.com/'

[issue_pr.observe]
type    = "exec"
command = "true"

[issue_pr.state_schema]
type = "object"

[issue_pr.state_schema.properties]
revision = { type = "string" }
`

const duplicateTaskDoc = `+++
[review]
kind              = "task"
description       = "Review a resource"
resource_observer = "issue_pr"
+++
Review it.
`

func loadDocsAndObservers(t *testing.T, cfg *Config) (map[string]TaskDocument, map[string]ResourceDef) {
	t.Helper()
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments: %v", err)
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		t.Fatalf("LoadResourceDefs: %v", err)
	}
	return docs, observers
}

// A plugin-shipped document resolves its own plugin's observer by a relative
// reference: both declarations sit in the same plugin layer, which is the
// namespace a relative reference resolves in.
func TestValidateTaskDocuments_PluginDocumentResolvesItsOwnObserver(t *testing.T) {
	pluginDir := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "plugin.toml"), `
name        = "acme"
version     = "0.1.0"
description = "test plugin"
`)
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(pluginDir, "config", "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "A plugin-shipped document reading its own plugin's observer"
resource_observer = "issue_pr"

[review.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
+++
Review it.
`)
	cfg := &Config{
		PluginDirs: []string{pluginDir},
		Plugins:    []plugins.Mounted{{ID: "official/plugins/acme", Dir: pluginDir}},
	}
	docs, observers := loadDocsAndObservers(t, cfg)
	if _, ok := docs["review"]; !ok {
		t.Fatalf("review not loaded: %+v", docs)
	}
	if err := cfg.ValidateTaskDocuments(docs, observers, nil); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}
}

// Ids share one namespace per layer across kinds, so a document and an effect
// declaring the same id in one layer is a load error rather than a silent
// choice of one of them.
func TestLoadTaskDeclarations_CollidesWithAnEffect(t *testing.T) {
	tests := []struct {
		name string
		// build writes the two declarations and returns the config that
		// reads them.
		build func(t *testing.T) *Config
	}{
		{
			name: "in one layer",
			build: func(t *testing.T) *Config {
				base := t.TempDir()
				writeFile(t, filepath.Join(base, "tasks", "review.md"), duplicateTaskDoc)
				writeFile(t, filepath.Join(base, "tasks", "review.toml"), collidingEffect)
				return &Config{BaseDir: base}
			},
		},
		{
			// The cascade lets a deeper layer replace a shallower same-id
			// declaration, but only of the same kind: a document does not
			// replace an effect a workflow node may still name.
			name: "a deeper document over a plugin effect",
			build: func(t *testing.T) *Config {
				pluginDir := t.TempDir()
				base := t.TempDir()
				writeFile(t, filepath.Join(pluginDir, "config", "tasks", "review.toml"), collidingEffect)
				writeFile(t, filepath.Join(base, "tasks", "review.md"), duplicateTaskDoc)
				return &Config{BaseDir: base, PluginDirs: []string{pluginDir}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := tt.build(t).LoadTaskDeclarations("")
			if err == nil {
				t.Fatal("expected a load error for one id declared by both a document and an effect")
			}
			if !strings.Contains(err.Error(), "review") {
				t.Errorf("error = %v, want it to name the id", err)
			}
		})
	}
}

const collidingEffect = `
[review]
kind  = "effect"
scope = "session"

[review.setup]
type   = "shell"
script = "true"
`

// The other half of the same rule: a user-owned document reaches catalog
// content only through the catalog-qualified dotted form, whose middle
// segments are the plugin's own path under its alias.
func TestValidateTaskDocuments_UserDocumentQualifiesACatalogObserver(t *testing.T) {
	pluginDir := t.TempDir()
	base := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "A user-owned document reading a catalog observer"
resource_observer = "official.acme.issue_pr"

[review.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
+++
Review it.
`)
	cfg := &Config{
		BaseDir:    base,
		PluginDirs: []string{pluginDir},
		Plugins:    []plugins.Mounted{{ID: "official/acme", Dir: pluginDir}},
	}
	docs, observers := loadDocsAndObservers(t, cfg)
	if err := cfg.ValidateTaskDocuments(docs, observers, nil); err != nil {
		t.Fatalf("ValidateTaskDocuments: %v", err)
	}
}

// A relative reference from user-owned config must not reach catalog content:
// its validity would then depend on which catalogs happen to be enabled.
func TestValidateTaskDocuments_UserDocumentCannotReachACatalogObserverRelatively(t *testing.T) {
	pluginDir := t.TempDir()
	base := t.TempDir()
	writeFile(t, filepath.Join(pluginDir, "config", "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "review.md"), `+++
[review]
kind              = "task"
description       = "A user-owned document reaching catalog content without its alias"
resource_observer = "issue_pr"

[review.done_when]
all = [{ check = "resource.state.revision", ne = "" }]
+++
Review it.
`)
	cfg := &Config{
		BaseDir:    base,
		PluginDirs: []string{pluginDir},
		Plugins:    []plugins.Mounted{{ID: "official/acme", Dir: pluginDir}},
	}
	docs, observers := loadDocsAndObservers(t, cfg)
	err := cfg.ValidateTaskDocuments(docs, observers, nil)
	if err == nil {
		t.Fatal("expected an unqualified reference to catalog content to be rejected")
	}
	if !strings.Contains(err.Error(), "catalog alias") {
		t.Errorf("error = %v, want it to say the reference needs its alias", err)
	}
}

// A chain's inputs are values over the chain-input roots, not templates: the
// parsed forms are what the runtime evaluates, and a literal stays a literal.
func TestLoadTaskDocuments_ChainInputsParseAsValues(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "pursue.md"), chainingTaskDoc)
	cfg := &Config{BaseDir: base}
	docs, err := cfg.LoadTaskDocuments("")
	if err != nil {
		t.Fatalf("LoadTaskDocuments: %v", err)
	}
	chains := docs["pursue"].Chains
	if len(chains) != 1 {
		t.Fatalf("chains = %+v, want one", chains)
	}
	ch := chains[0]
	if ch.ID != "goal_review" || ch.Workflow != "goal_reviewer" || ch.EffectivePlacement() != "sibling" {
		t.Errorf("chain = %+v", ch)
	}
	if ch.TaskID != "pursue" {
		t.Errorf("chain names its declaring document: got %q", ch.TaskID)
	}
	if len(ch.When.All) != 1 || ch.When.All[0].JudgePending != "goal-met" {
		t.Errorf("when = %+v", ch.When)
	}
	if got := ch.Inputs["task"]; got == nil || got.Literal != "goal_review" {
		t.Errorf("a literal input stays a literal: %+v", got)
	}
	if got := ch.Inputs["work_session"]; got == nil || got.From != "task.session" {
		t.Errorf("a projection keeps its path: %+v", got)
	}
	if got := ch.InputKeys(); len(got) != 2 || got[0] != "task" || got[1] != "work_session" {
		t.Errorf("InputKeys = %v, want them sorted", got)
	}
}

// One chain id names one chain within its document.
func TestLoadTaskDocuments_RejectsDuplicateChainID(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "pursue.md"), strings.Replace(chainingTaskDoc,
		"+++\nPursue", `
[[pursue.chains]]
id       = "goal_review"
workflow = "goal_reviewer"

[pursue.chains.when]
all = [{ judge_pending = "goal-met" }]
+++
Pursue`, 1))
	cfg := &Config{BaseDir: base}
	if _, err := cfg.LoadTaskDocuments(""); err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("err = %v, want a duplicate chain id rejection", err)
	}
}

// A chain with no trigger would fire unconditionally.
func TestLoadTaskDocuments_RejectsChainWithNoTrigger(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "resources", "issue_pr.toml"), minimalObserver)
	writeFile(t, filepath.Join(base, "tasks", "pursue.md"), `+++
[pursue]
kind              = "task"
description       = "A document whose chain declares no trigger"
resource_observer = "issue_pr"

[[pursue.chains]]
id       = "goal_review"
workflow = "goal_reviewer"
+++
Pursue the goal.
`)
	cfg := &Config{BaseDir: base}
	if _, err := cfg.LoadTaskDocuments(""); err == nil || !strings.Contains(err.Error(), "declares no facts") {
		t.Fatalf("err = %v, want a triggerless chain rejection", err)
	}
}

const chainingTaskDoc = `+++
[pursue]
kind              = "task"
description       = "Pursue one goal until an independent reviewer confirms it"
resource_observer = "issue_pr"

[pursue.done_when]
all = [{ judge = "the goal is achieved", id = "goal-met" }]

[[pursue.chains]]
id        = "goal_review"
workflow  = "goal_reviewer"
placement = "sibling"

[pursue.chains.when]
all = [{ judge_pending = "goal-met" }]

[pursue.chains.inputs]
task         = "goal_review"
work_session = { from = "task.session" }
+++
Pursue the goal at {{ resource.id }}.
`
