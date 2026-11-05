package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func TestWorkflowRevisionCanonicalBodyAndRoundTrip(t *testing.T) {
	first := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Nodes: []WorkflowNode{{ID: "one", Uses: "effect"}}, Populations: []WorkflowPopulation{{Name: "dynamic"}}, Healthcheck: &HealthcheckConfig{Period: Duration{Duration: time.Second}}},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect", Scope: TaskScopeRun}},
		Tasks:    map[string]TaskDocument{"task": {ID: "task", Instruction: "Do the work."}},
	}
	second := WorkflowRevision{
		Workflow: first.Workflow,
		Effects:  map[string]TaskDefinition{"effect": {Scope: TaskScopeRun, ID: "effect"}},
		Tasks:    map[string]TaskDocument{"task": {Instruction: "Do the work.", ID: "task"}},
	}
	oneBody, oneDigest, err := first.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	twoBody, twoDigest, err := second.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if string(oneBody) != string(twoBody) || oneDigest != twoDigest {
		t.Fatalf("equal declarations differ: %q/%s, %q/%s", oneBody, oneDigest, twoBody, twoDigest)
	}
	if strings.Contains(string(oneBody), "dynamic") {
		t.Fatalf("population declaration entered revision: %s", oneBody)
	}
	decoded, err := DecodeWorkflowRevision(oneBody, oneDigest)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Tasks["task"].Instruction != "Do the work." || decoded.Effects["effect"].Scope != TaskScopeRun {
		t.Fatalf("revision lost declarations: %+v", decoded)
	}
	if decoded.Workflow.Healthcheck.Period.Duration != time.Second {
		t.Fatalf("healthcheck period = %v", decoded.Workflow.Healthcheck.Period.Duration)
	}
	if _, err := DecodeWorkflowRevision(oneBody, strings.Repeat("0", 64)); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestDecodedRevisionCacheDecodesDigestOnce(t *testing.T) {
	revision := WorkflowRevision{Workflow: WorkflowFile{ID: "work", Address: "work"}}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	decodes := 0
	cache := decodedRevisionCache{decode: func(body []byte, digest string) (WorkflowRevision, error) {
		decodes++
		return DecodeWorkflowRevision(body, digest)
	}}
	for range 2 {
		got, err := cache.load(body, digest)
		if err != nil || got.Workflow.Address != "work" {
			t.Fatalf("cache lookup = %+v, %v", got, err)
		}
	}
	if decodes != 1 {
		t.Fatalf("decode calls = %d, want 1", decodes)
	}
}

func TestDecodedRevisionCacheReturnsIndependentTopLevelDeclarations(t *testing.T) {
	revision := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work"},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect"}},
		Tasks:    map[string]TaskDocument{"task": {ID: "task"}},
		Channels: map[string]ChannelDefinition{"channel": {ID: "channel"}},
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	cache := decodedRevisionCache{decode: DecodeWorkflowRevision}
	first, err := cache.load(body, digest)
	if err != nil {
		t.Fatal(err)
	}
	first.Effects["effect"] = TaskDefinition{ID: "changed"}
	first.Tasks["task"] = TaskDocument{ID: "changed"}
	first.Channels["channel"] = ChannelDefinition{ID: "changed"}
	second, err := cache.load(body, digest)
	if err != nil {
		t.Fatal(err)
	}
	if second.Effects["effect"].ID != "effect" || second.Tasks["task"].ID != "task" || second.Channels["channel"].ID != "channel" {
		t.Fatalf("cache lookup was changed by caller: %+v", second)
	}
}

func TestWorkflowRevisionPluginSourceSurvivesMountChange(t *testing.T) {
	oldDir := filepath.Join(t.TempDir(), "old")
	newDir := filepath.Join(t.TempDir(), "new")
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "workflows", "work.toml"), "[work]\nkind = \"workflow\"\nresource = \"issue\"\n[[work.nodes]]\nuses = \"catalog.agent.effect\"\n")
	for _, dir := range []string{oldDir, newDir} {
		writeFile(t, filepath.Join(dir, "config", "tasks", "effect.toml"), "[effect]\nkind = \"effect\"\nscope = \"run\"\n")
	}
	mount := func(dir string) plugins.Mounted {
		return plugins.Mounted{ID: "catalog/agent", Dir: dir, Manifest: plugins.Manifest{Executables: []plugins.Executable{{Name: "runner", Path: "bin/runner"}}}}
	}
	oldConfig := &Config{BaseDir: base, PluginDirs: []string{oldDir}, Plugins: []plugins.Mounted{mount(oldDir)}}
	revision, err := oldConfig.CaptureWorkflowRevision("", "work")
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkflowRevision(body, digest)
	if err != nil {
		t.Fatal(err)
	}
	newConfig := &Config{BaseDir: base, PluginDirs: []string{newDir}, Plugins: []plugins.Mounted{mount(newDir)}}
	newRevision, err := newConfig.CaptureWorkflowRevision("", "work")
	if err != nil {
		t.Fatal(err)
	}
	_, newDigest, err := newRevision.CanonicalBody()
	if err != nil || newDigest != digest {
		t.Fatalf("mount changed revision digest: %q, %v", newDigest, err)
	}
	def := decoded.Effects["catalog.agent.effect"]
	if def.SourcePath != "plugin://catalog%2Fagent/config/tasks/effect.toml" {
		t.Fatalf("stored plugin source = %q", def.SourcePath)
	}
	bins := MountedBins{Mounted: []plugins.Mounted{mount(newDir)}, SourcePath: def.SourcePath}
	got, err := bins.ResolveBin("runner", def.Ownership())
	if err != nil || got != filepath.Join(newDir, "bin", "runner") {
		t.Fatalf("resolve after mount change = %q, %v", got, err)
	}
	if strings.Contains(string(body), oldDir) {
		t.Fatalf("revision captured old mount directory: %s", body)
	}
}

func TestWorkflowRevisionUncataloguedPluginSourceDoesNotPinDirectory(t *testing.T) {
	revision := WorkflowRevision{Effects: map[string]TaskDefinition{
		"effect": {FromPlugin: true, SourcePath: "/local/plugin/config/tasks/effect.toml"},
	}}
	if err := revision.PortablePluginSources(nil); err != nil {
		t.Fatal(err)
	}
	if got := revision.Effects["effect"].SourcePath; got != "" {
		t.Fatalf("uncatalogued plugin source = %q, want no host path", got)
	}
}

func TestSessionWithWorkflowRequiresStoredRevision(t *testing.T) {
	session := &domain.Session{Name: "pending", Workflow: "work"}
	cfg := &Config{}
	if _, _, err := RevisionForSession(session); err == nil {
		t.Fatal("missing revision accepted")
	}
	if _, err := cfg.ResolveSessionWorkflow(session); err == nil {
		t.Fatal("workflow resolved from live configuration")
	}
	if _, err := cfg.TaskDefinitionsForSession(session); err == nil {
		t.Fatal("effects resolved from live configuration")
	}
	if _, _, err := cfg.TaskDeclarationsForSession(session); err == nil {
		t.Fatal("tasks resolved from live configuration")
	}
	if _, err := cfg.ChannelsForSession(session); err == nil {
		t.Fatal("channels resolved from live configuration")
	}
}

func TestSessionWithoutWorkflowReadsCurrentTaskDeclarations(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), "[work]\nkind = \"task\"\ndescription = \"work\"\ninstructions = [{ text = \"Do the work.\" }]\n")
	cfg := &Config{BaseDir: base}
	docs, _, err := cfg.TaskDeclarationsForSession(&domain.Session{Name: "ad-hoc"})
	if err != nil || docs["work"].ID != "work" {
		t.Fatalf("ad-hoc task declarations = %+v, %v", docs, err)
	}
}

func TestWorkflowRevisionDigestIgnoresNonExecutableSourceLocations(t *testing.T) {
	first := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", SourcePath: "/workspace/one/work.toml", BaseDir: "/workspace/one", Display: map[string]*lang.Value{
			"title": {Form: lang.FormLiteral, Literal: "work", Pos: lang.Position{File: "/workspace/one/work.toml", Path: "work.display.title"}},
		}},
		Effects: map[string]TaskDefinition{"effect": {ID: "effect", SourcePath: "/workspace/one/effect.toml", BaseDir: "/workspace/one"}},
		Tasks:   map[string]TaskDocument{"task": {ID: "task", SourcePath: "/workspace/one/task.toml", BaseDir: "/workspace/one"}},
	}
	second := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", SourcePath: "/workspace/two/work.toml", BaseDir: "/workspace/two", Display: map[string]*lang.Value{
			"title": {Form: lang.FormLiteral, Literal: "work", Pos: lang.Position{File: "/workspace/two/work.toml", Path: "work.display.title"}},
		}},
		Effects: map[string]TaskDefinition{"effect": {ID: "effect", SourcePath: "/workspace/two/effect.toml", BaseDir: "/workspace/two"}},
		Tasks:   map[string]TaskDocument{"task": {ID: "task", SourcePath: "/workspace/two/task.toml", BaseDir: "/workspace/two"}},
	}
	_, oneDigest, err := first.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	_, twoDigest, err := second.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if oneDigest != twoDigest {
		t.Fatalf("same declarations at different paths got digests %q and %q", oneDigest, twoDigest)
	}
}

func TestWorkflowRevisionCanonicalBodyKeepsUTF8AndHTMLCharacters(t *testing.T) {
	revision := WorkflowRevision{Workflow: WorkflowFile{ID: "work", Address: "work", Description: "<>& café"}}
	body, _, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "<>& café") {
		t.Fatalf("canonical body escaped declaration text: %s", body)
	}
}

func TestSessionWorkflowAndRunScopeUseStoredRevision(t *testing.T) {
	revision := WorkflowRevision{
		Workflow: WorkflowFile{ID: "work", Address: "work", Nodes: []WorkflowNode{{ID: "original", Uses: "effect"}}},
		Effects:  map[string]TaskDefinition{"effect": {ID: "effect", Scope: TaskScopeRun}},
	}
	body, digest, err := revision.CanonicalBody()
	if err != nil {
		t.Fatal(err)
	}
	session := &domain.Session{Workflow: "work", WorkflowRevision: digest, WorkflowRevisionBody: string(body),
		Nodes: map[string]*contract.TaskState{"original": {Scope: TaskScopeRun, Status: contract.TaskStatusProduced}}}
	cfg := &Config{}
	wf, err := cfg.ResolveSessionWorkflow(session)
	if err != nil || wf == nil || len(wf.Nodes) != 1 || wf.Nodes[0].ID != "original" {
		t.Fatalf("stored workflow = %+v, err %v", wf, err)
	}
	if !cfg.RunScopeUp(session) {
		t.Fatal("stored run-scoped node was ignored")
	}
}
