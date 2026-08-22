package config

import (
	"path/filepath"
	"strings"
	"testing"
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
