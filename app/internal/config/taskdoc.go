package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// TaskDocument is one `kind = "task"` declaration and the instruction body
// below its frontmatter. It is the work — one piece of it, made explicit
// enough to hand over: what to do, what would make it done, what it is about,
// and what follows from it.
//
// A task document owns no lifecycle. It has no setup, no cleanup, no health
// probe, no interactive endpoint and no nesting joint — those are an effect's
// concerns, and a task document is dispatched into a session a workflow has
// already built.
type TaskDocument struct {
	ID          string
	Description string
	// ResourceObserver is the observer this document is written for. It closes
	// the completion chain at load: the observer's state_schema says which
	// keys exist, so a `resource.state.*` a gate reads is resolved against a
	// declaration rather than discovered at run time.
	ResourceObserver string
	InputsSchema     map[string]any
	InputsSchemaFile string
	// StateSchema declares this document's own state: the keys a reviewer or
	// another session records into an instance, read as `self.state.*`. It
	// carries no mutability annotation — state is mutable by definition.
	StateSchema     map[string]any
	StateSchemaFile string
	DoneWhen        *DoneWhen
	// Budget bounds convergence. Omitting it leaves completion unbounded,
	// which is what a standing goal needs: continuing to exist is not
	// exhaustion.
	Budget map[string]any
	Chains []DocumentChain
	// Instruction is the body below the closing `+++`. Its `{{ <path> }}`
	// projections are part of the language, validated at load against the
	// roots this surface declares.
	Instruction string
	BaseDir     string
	SourcePath  string
	FromPlugin  bool
	// PluginLayer identifies the plugin layer this declaration was mounted
	// from: see Config.pluginLayerOf.
	PluginLayer string
	// Definition is the parsed declaration this document was read from. It is
	// kept so contract validation and reference resolution work from the
	// declaration itself rather than re-reading the file, which would decide
	// twice what the document already said once.
	Definition *lang.Definition
}

// Ownership names the layer that wrote this declaration, for the reference
// rules that differ between shipped and user-authored config. A plugin's own
// reference is relative and resolves in that plugin's namespace, so the
// ownership has to name which plugin — not merely that there is one.
func (d TaskDocument) Ownership() lang.Ownership {
	if !d.FromPlugin {
		return lang.Ownership{}
	}
	return pluginOwnership(d.PluginLayer)
}

// ResolvedInputsSchemaPath / ResolvedStateSchemaPath join a schema file with
// BaseDir.
func (d TaskDocument) ResolvedInputsSchemaPath() string {
	return resolveSchemaPath(d.InputsSchemaFile, d.BaseDir)
}

func (d TaskDocument) ResolvedStateSchemaPath() string {
	return resolveSchemaPath(d.StateSchemaFile, d.BaseDir)
}

// LoadTaskDocuments loads every task document in the trusted base layers.
// Task documents share the effect root — a directory name is not semantic, so
// what separates the two is the serialization the language assigns each kind:
// a kind with a body is a Markdown file, a kind without one is TOML.
func (c *Config) LoadTaskDocuments(workspaceDirPath string) (map[string]TaskDocument, error) {
	out := make(map[string]TaskDocument)
	pluginOwner := make(map[string]string)
	for _, layer := range c.tasksSearchDirs(workspaceDirPath) {
		entries, err := listMarkdownFiles(layer.dir)
		if err != nil {
			return nil, err
		}
		if layer.workspaceDir && len(entries) > 0 {
			return nil, fmt.Errorf("task documents inside the workspace directory are not loaded (clone content must not declare the work it is about): %s; move them to the global layer, a plugin, or a repo overlay above the workspace dir", entries[0])
		}
		layerOwner := make(map[string]string)
		for _, path := range entries {
			doc, err := c.loadTaskDocument(path, layer.plugin)
			if err != nil {
				return nil, err
			}
			if prior, dup := layerOwner[doc.ID]; dup {
				return nil, lang.DuplicateID(doc.ID, prior, path)
			}
			layerOwner[doc.ID] = path
			if layer.plugin {
				if owner, exists := pluginOwner[doc.ID]; exists {
					return nil, fmt.Errorf("task %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", doc.ID, owner, path)
				}
				pluginOwner[doc.ID] = path
			}
			out[doc.ID] = doc
		}
	}
	return out, nil
}

// loadTaskDocument reads one Markdown definition document. Contract
// validation — the observer reference, and every completion key against the
// two schemas that declare them — needs the rest of the layer, so it runs
// once the whole set is loaded (see ValidateTaskDocuments).
func (c *Config) loadTaskDocument(path string, fromPlugin bool) (TaskDocument, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return TaskDocument{}, err
	}
	def, err := lang.ParseTaskDocument(path, src)
	if err != nil {
		return TaskDocument{}, err
	}
	if def.Kind != lang.KindTask {
		return TaskDocument{}, fmt.Errorf("%s: %q declares kind %q; a Markdown definition document declares a task", path, def.ID, def.Kind)
	}
	doc, err := taskDocumentFrom(def, path, fromPlugin)
	if err != nil {
		return TaskDocument{}, fmt.Errorf("task %s in %s: %w", def.ID, path, err)
	}
	if fromPlugin {
		doc.PluginLayer = c.pluginLayerOf(path)
	}
	validation := lang.Validation{From: doc.Ownership(), Executables: c.binResolver(path)}
	if err := validation.ValidateDefinition(def); err != nil {
		return TaskDocument{}, err
	}
	doc.Definition = def
	return doc, nil
}

// taskDocumentFrom reads the fields the runtime needs off a validated
// declaration. The typed decoders for the completion predicate and the chains
// already own those fields' shape, so the subtree is re-encoded and handed to
// them rather than walked again here.
func taskDocumentFrom(def *lang.Definition, path string, fromPlugin bool) (TaskDocument, error) {
	d := TaskDocument{
		ID:          def.ID,
		Instruction: def.Instruction,
		SourcePath:  path,
		BaseDir:     configFileDir(path),
		FromPlugin:  fromPlugin,
	}
	for _, field := range []struct {
		key    string
		target *string
	}{
		{"description", &d.Description},
		{"resource_observer", &d.ResourceObserver},
		{"inputs_schema_file", &d.InputsSchemaFile},
		{"state_schema_file", &d.StateSchemaFile},
	} {
		if raw, ok := def.Body[field.key]; ok {
			value, ok := raw.(string)
			if !ok {
				return d, fmt.Errorf("`%s` is a string", field.key)
			}
			*field.target = value
		}
	}
	for _, field := range []struct {
		key    string
		target *map[string]any
	}{
		{"inputs_schema", &d.InputsSchema},
		{"state_schema", &d.StateSchema},
		{"budget", &d.Budget},
	} {
		if raw, ok := def.Body[field.key]; ok {
			table, ok := raw.(map[string]any)
			if !ok {
				return d, fmt.Errorf("`%s` is a table", field.key)
			}
			*field.target = table
		}
	}
	doneWhen, err := decodeDoneWhen(def)
	if err != nil {
		return d, err
	}
	d.DoneWhen = doneWhen
	// A document declares its budget beside its completion predicate, because
	// what it bounds is convergence, not one predicate's shape; the runtime
	// accounts for patience per predicate, so the two are joined here.
	if d.DoneWhen != nil && len(d.Budget) > 0 {
		d.DoneWhen.Budget = d.Budget
	}
	if err := d.DoneWhen.Validate(); err != nil {
		return d, err
	}
	if d.Chains, err = documentChainsFrom(def, path); err != nil {
		return d, err
	}
	return d, nil
}

// decodeDoneWhen re-encodes the completion subtree and decodes it with the
// typed decoder that already owns that field's shape, rather than duplicating
// it against the parsed tree.
func decodeDoneWhen(def *lang.Definition) (*DoneWhen, error) {
	raw, ok := def.Body["done_when"]
	if !ok {
		return nil, nil
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(map[string]any{"done_when": raw}); err != nil {
		return nil, err
	}
	var decoded struct {
		DoneWhen *DoneWhen `toml:"done_when"`
	}
	if _, err := toml.Decode(encoded.String(), &decoded); err != nil {
		return nil, err
	}
	return decoded.DoneWhen, nil
}

// listMarkdownFiles returns sorted *.md entries in dir. A missing dir returns
// an empty list, so a layer that declares no task document is normal.
func listMarkdownFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// ValidateTaskDocuments runs the contract checks that need the rest of the
// layer: the observer a document declares, every completion key against the
// two state contracts that declare them, each `when` judge id against the
// document's own completion predicate, chain inputs against both live roots,
// and each chain's target workflow against its own inputs contract.
//
// It is separate from loading for the reason lang states: root availability,
// which needs nothing but the document, is decided first, so a value reaching
// a root its surface does not offer is reported as that rather than as a
// missing key in a contract the root never had.
func (c *Config) ValidateTaskDocuments(docs map[string]TaskDocument, observers map[string]ResourceDef, workflows map[string]WorkflowFile) error {
	registry := c.taskReferenceRegistry(docs, observers, workflows)
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		doc := docs[id]
		validation := lang.Validation{From: doc.Ownership(), Executables: c.binResolver(doc.SourcePath)}
		if err := validation.ValidateTaskContracts(doc.Definition, registry); err != nil {
			return err
		}
	}
	return nil
}

// taskReferenceRegistry is the namespace a task document's references resolve
// against. Observers and task documents contribute their own declarations.
//
// A workflow contributes a declaration assembled from what the workflow
// loader decoded — its id, its kind, and its inputs contract — because the
// workflow surface has not moved onto the ratified language yet. That is the
// workflow's own declaration read through the legacy decoder, and it is what
// lets a chain's target be resolved and its inputs checked against that
// target's contract at load. Retirement: the PR that moves workflows onto the
// ratified language deletes this construction and registers the parsed
// definitions instead.
func (c *Config) taskReferenceRegistry(docs map[string]TaskDocument, observers map[string]ResourceDef, workflows map[string]WorkflowFile) *lang.Registry {
	var user []*lang.Definition
	byLayer := map[lang.Ownership][]*lang.Definition{}
	add := func(def *lang.Definition, sourcePath string) {
		if def == nil {
			return
		}
		// Where a declaration is registered follows from where it was
		// mounted, not from what kind it is: a plugin's relative reference
		// resolves in its own layer, so its declarations have to be in that
		// layer and nowhere else.
		layer := c.pluginLayerOf(sourcePath)
		if layer == "" {
			user = append(user, def)
			return
		}
		from := pluginOwnership(layer)
		byLayer[from] = append(byLayer[from], def)
	}
	for _, observer := range observers {
		add(observer.Definition, observer.SourcePath)
	}
	for _, doc := range docs {
		add(doc.Definition, doc.SourcePath)
	}
	for id, wf := range workflows {
		add(&lang.Definition{
			ID:   id,
			Kind: lang.KindWorkflow,
			File: wf.SourcePath,
			Body: map[string]any{"inputs_schema": wf.InputsSchema},
		}, wf.SourcePath)
	}
	layers := make([]lang.PluginLayer, 0, len(byLayer))
	for from, defs := range byLayer {
		layers = append(layers, lang.PluginLayer{Alias: from.Alias, Path: from.Path, Defs: defs})
	}
	return lang.NewRegistry(layers, user)
}

// LoadTaskDeclarations loads everything the `tasks/` root declares — task
// documents and effects alike — and enforces the rule neither loader can see
// on its own: an id names one declaration.
//
// It is the entry point for any caller that reads a completion predicate,
// because which kind an id resolves to is exactly what such a caller is
// asking, and answering it from one of the two maps alone would make a
// collision a silent choice.
func (c *Config) LoadTaskDeclarations(workspaceDirPath string) (map[string]TaskDocument, map[string]TaskDefinition, error) {
	docs, err := c.LoadTaskDocuments(workspaceDirPath)
	if err != nil {
		return nil, nil, err
	}
	effects, err := c.LoadTaskDefinitions(workspaceDirPath)
	if err != nil {
		return nil, nil, err
	}
	if err := validateTaskIDNamespace(docs, effects); err != nil {
		return nil, nil, err
	}
	return docs, effects, nil
}

// validateTaskIDNamespace rejects one id declared by both kinds. A deeper
// layer replacing a shallower same-id declaration is the cascade rule only
// when both declare the same kind: replacing an effect with a task document
// would silently drop a lifecycle a workflow node still names.
func validateTaskIDNamespace(docs map[string]TaskDocument, effects map[string]TaskDefinition) error {
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		effect, clash := effects[id]
		if !clash {
			continue
		}
		return fmt.Errorf("id %q is declared as a task by %s and as an effect by %s; an id names one declaration, and a task document does not replace an effect a workflow node may still name", id, docs[id].SourcePath, effect.SourcePath)
	}
	return nil
}
