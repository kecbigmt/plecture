package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

type WorkflowRevision struct {
	Workflow WorkflowFile                 `json:"workflow"`
	Effects  map[string]TaskDefinition    `json:"effects"`
	Tasks    map[string]TaskDocument      `json:"tasks"`
	Channels map[string]ChannelDefinition `json:"channels,omitempty"`
}

func (r *WorkflowRevision) PortablePluginSources(mounted []plugins.Mounted) error {
	var source func(string) (string, error)
	source = func(path string) (string, error) {
		var owner plugins.Mounted
		var relative string
		for _, plugin := range mounted {
			if plugin.Dir == "" {
				continue
			}
			rel, err := filepath.Rel(plugin.Dir, path)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if len(plugin.Dir) > len(owner.Dir) {
					owner, relative = plugin, rel
				}
			}
		}
		if owner.Dir != "" {
			return "plugin://" + url.PathEscape(owner.ID) + "/" + filepath.ToSlash(relative), nil
		}
		// A directory without a catalog identity cannot resolve a bare executable
		// through the mounted-plugin set, so its absolute path has no runtime role.
		return "", nil
	}
	var convertEffect func(TaskDefinition) (TaskDefinition, error)
	convertEffect = func(def TaskDefinition) (TaskDefinition, error) {
		if def.FromPlugin {
			var err error
			def.SourcePath, err = source(def.SourcePath)
			if err != nil {
				return def, err
			}
		}
		for i := range def.InnerChain {
			var err error
			def.InnerChain[i], err = convertEffect(def.InnerChain[i])
			if err != nil {
				return def, err
			}
		}
		return def, nil
	}
	for key, def := range r.Effects {
		var err error
		r.Effects[key], err = convertEffect(def)
		if err != nil {
			return fmt.Errorf("effect %q: %w", key, err)
		}
	}
	for key, channel := range r.Channels {
		if channel.FromPlugin {
			var err error
			channel.SourcePath, err = source(channel.SourcePath)
			if err != nil {
				return fmt.Errorf("channel %q: %w", key, err)
			}
			r.Channels[key] = channel
		}
	}
	return nil
}

func (r WorkflowRevision) CanonicalBody() ([]byte, string, error) {
	// Population is evaluated from the live configuration, independently of
	// the session's executable revision.
	r.Workflow.Populations = nil
	r.Workflow.Definition = nil
	r.Workflow.BaseDir = ""
	r.Workflow.SourcePath = ""
	if r.Effects != nil {
		effects := make(map[string]TaskDefinition, len(r.Effects))
		for key, def := range r.Effects {
			effects[key] = canonicalEffect(def)
		}
		r.Effects = effects
	}
	if r.Tasks != nil {
		tasks := make(map[string]TaskDocument, len(r.Tasks))
		for key, doc := range r.Tasks {
			tasks[key] = canonicalTask(doc)
		}
		r.Tasks = tasks
	}
	if r.Channels != nil {
		channels := make(map[string]ChannelDefinition, len(r.Channels))
		for key, channel := range r.Channels {
			if !channel.FromPlugin {
				channel.SourcePath = ""
			}
			channels[key] = channel
		}
		r.Channels = channels
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(r); err != nil {
		return nil, "", fmt.Errorf("encode workflow revision: %w", err)
	}
	body := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

func canonicalEffect(def TaskDefinition) TaskDefinition {
	def.BaseDir = ""
	if !def.FromPlugin {
		def.SourcePath = ""
	}
	if len(def.InnerChain) > 0 {
		chain := make([]TaskDefinition, len(def.InnerChain))
		for i, inner := range def.InnerChain {
			chain[i] = canonicalEffect(inner)
		}
		def.InnerChain = chain
	}
	return def
}

func canonicalTask(doc TaskDocument) TaskDocument {
	doc.Definition = nil
	doc.BaseDir = ""
	doc.SourcePath = ""
	if len(doc.ExtendsLayers) > 0 {
		layers := make([]TaskDocument, len(doc.ExtendsLayers))
		for i, layer := range doc.ExtendsLayers {
			layers[i] = canonicalTask(layer)
		}
		doc.ExtendsLayers = layers
	}
	return doc
}

func DecodeWorkflowRevision(body []byte, digest string) (WorkflowRevision, error) {
	sum := sha256.Sum256(body)
	if digest != hex.EncodeToString(sum[:]) {
		return WorkflowRevision{}, fmt.Errorf("workflow revision digest mismatch")
	}
	var revision WorkflowRevision
	if err := json.Unmarshal(body, &revision); err != nil {
		return WorkflowRevision{}, fmt.Errorf("decode workflow revision: %w", err)
	}
	return revision, nil
}

type decodedRevisionCache struct {
	mu      sync.Mutex
	entries map[string]WorkflowRevision
	decode  func([]byte, string) (WorkflowRevision, error)
}

func copyRevisionDeclarations(revision WorkflowRevision) WorkflowRevision {
	revision.Effects = maps.Clone(revision.Effects)
	revision.Tasks = maps.Clone(revision.Tasks)
	revision.Channels = maps.Clone(revision.Channels)
	return revision
}

func (c *decodedRevisionCache) load(body []byte, digest string) (WorkflowRevision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if revision, ok := c.entries[digest]; ok {
		return copyRevisionDeclarations(revision), nil
	}
	revision, err := c.decode(body, digest)
	if err != nil {
		return WorkflowRevision{}, err
	}
	if c.entries == nil {
		c.entries = make(map[string]WorkflowRevision)
	}
	c.entries[digest] = revision
	return copyRevisionDeclarations(revision), nil
}

// A digest identifies immutable content, so each process keeps one decoded entry per distinct digest without eviction.
var decodedRevisions = decodedRevisionCache{decode: DecodeWorkflowRevision}

func RevisionForSession(s *domain.Session) (WorkflowRevision, bool, error) {
	if s == nil {
		return WorkflowRevision{}, false, fmt.Errorf("session is nil")
	}
	if s.WorkflowRevision == "" && s.Workflow != "" {
		return WorkflowRevision{}, false, fmt.Errorf("session %q has no stored workflow revision", s.Name)
	}
	if s.WorkflowRevision == "" {
		return WorkflowRevision{}, false, nil
	}
	r, err := decodedRevisions.load([]byte(s.WorkflowRevisionBody), s.WorkflowRevision)
	if err != nil {
		return WorkflowRevision{}, true, err
	}
	if r.Workflow.Address != s.Workflow {
		return WorkflowRevision{}, true, fmt.Errorf("session workflow %q differs from revision workflow %q", s.Workflow, r.Workflow.Address)
	}
	return r, true, nil
}

func (c *Config) TaskDefinitionsForSession(s *domain.Session) (map[string]TaskDefinition, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		return r.Effects, err
	}
	return c.LoadTaskDefinitions(s.ProjectRoot)
}

func (c *Config) TaskDeclarationsForSession(s *domain.Session) (map[string]TaskDocument, map[string]TaskDefinition, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		return r.Tasks, r.Effects, err
	}
	return c.LoadTaskDeclarations(s.ProjectRoot)
}

func (c *Config) ChannelsForSession(s *domain.Session) (map[string]ChannelDefinition, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		return r.Channels, err
	}
	return c.LoadChannels()
}

func (c *Config) CaptureWorkflowRevision(projectRoot, name string) (WorkflowRevision, error) {
	workflows, err := c.LoadWorkflows(projectRoot)
	if err != nil {
		return WorkflowRevision{}, err
	}
	wf, ok := workflows[name]
	if !ok {
		return WorkflowRevision{}, fmt.Errorf("workflow %q not found", name)
	}
	wf.Definition = nil
	wf.Populations = nil
	if wf.InputsSchemaFile != "" {
		wf.InputsSchema, err = loadRawSchema(nil, wf.ResolvedInputsSchemaPath())
		if err != nil {
			return WorkflowRevision{}, err
		}
		wf.InputsSchemaFile = ""
	}
	effects, err := c.LoadTaskDefinitions(projectRoot)
	if err != nil {
		return WorkflowRevision{}, err
	}
	for key, def := range effects {
		def, err = freezeEffectSchemas(def)
		if err != nil {
			return WorkflowRevision{}, fmt.Errorf("effect %q: %w", key, err)
		}
		effects[key] = def
	}
	if err := c.validateKnownNodeSubscriptions(wf, effects); err != nil {
		return WorkflowRevision{}, err
	}
	tasks, err := c.LoadTaskDocuments(projectRoot)
	if err != nil {
		return WorkflowRevision{}, err
	}
	if len(tasks) > 0 {
		observers, err := c.LoadResourceDefs()
		if err != nil {
			return WorkflowRevision{}, err
		}
		if err := c.ValidateTaskDocuments(tasks, observers, workflows); err != nil {
			return WorkflowRevision{}, err
		}
	}
	for key, doc := range tasks {
		doc, err = freezeTaskSchemas(doc)
		if err != nil {
			return WorkflowRevision{}, fmt.Errorf("task %q: %w", key, err)
		}
		doc.Definition = nil
		tasks[key] = doc
	}
	revision := WorkflowRevision{Workflow: wf, Effects: effects, Tasks: tasks}
	if len(wf.Event.Channel) > 0 {
		channels, err := c.LoadChannels()
		if err != nil {
			return WorkflowRevision{}, err
		}
		revision.Channels = make(map[string]ChannelDefinition)
		for _, binding := range wf.Event.Channel {
			channel, ok := channels[binding.Uses]
			if !ok {
				return WorkflowRevision{}, fmt.Errorf("workflow %q: event channel %q not found", name, binding.Uses)
			}
			revision.Channels[binding.Uses] = channel
		}
	}
	if err := revision.PortablePluginSources(c.Plugins); err != nil {
		return WorkflowRevision{}, err
	}
	return revision, nil
}

func (c *Config) validateKnownNodeSubscriptions(wf WorkflowFile, effects map[string]TaskDefinition) error {
	var resources map[string]ResourceDef
	for _, node := range wf.Nodes {
		if node.Subscription == "" {
			continue
		}
		def, ok := effects[node.Uses]
		if !ok {
			continue
		}
		properties, _ := def.OutputsSchema["properties"].(map[string]any)
		property, _ := properties[node.Subscription].(map[string]any)
		id, _ := property["const"].(string)
		if id == "" {
			continue
		}
		if resources == nil {
			var err error
			resources, err = c.LoadResourceDefs()
			if err != nil {
				return err
			}
		}
		for _, resource := range resources {
			matched, err := regexp.MatchString(resource.Match, id)
			if err != nil {
				return fmt.Errorf("resource %q match: %w", resource.ID, err)
			}
			if !matched || resource.Subscribe == nil || resource.InputsSchema == nil {
				continue
			}
			schema, err := lang.CompileSchema(resource.InputsSchema, "", "plect:resource:"+resource.ID+":inputs")
			if err != nil {
				return fmt.Errorf("node %q subscription resource %q inputs schema: %w", node.ID, resource.ID, err)
			}
			if err := schema.Validate(map[string]any{}); err != nil {
				return fmt.Errorf("node %q subscription resource %q requires action inputs: %w", node.ID, resource.ID, err)
			}
		}
	}
	return nil
}

func freezeEffectSchemas(def TaskDefinition) (TaskDefinition, error) {
	var err error
	if def.InputsSchemaFile != "" {
		def.InputsSchema, err = loadRawSchema(nil, def.ResolvedInputsSchemaPath())
		if err != nil {
			return def, err
		}
		def.InputsSchemaFile = ""
	}
	if def.OutputsSchemaFile != "" {
		def.OutputsSchema, err = loadRawSchema(nil, def.ResolvedOutputsSchemaPath())
		if err != nil {
			return def, err
		}
		def.OutputsSchemaFile = ""
	}
	if def.LocalsSchemaFile != "" {
		def.LocalsSchema, err = loadRawSchema(nil, def.ResolvedLocalsSchemaPath())
		if err != nil {
			return def, err
		}
		def.LocalsSchemaFile = ""
	}
	for i, inner := range def.InnerChain {
		def.InnerChain[i], err = freezeEffectSchemas(inner)
		if err != nil {
			return def, err
		}
	}
	return def, nil
}

func freezeTaskSchemas(doc TaskDocument) (TaskDocument, error) {
	var err error
	if doc.InputsSchemaFile != "" {
		doc.InputsSchema, err = loadRawSchema(nil, doc.ResolvedInputsSchemaPath())
		if err != nil {
			return doc, err
		}
		doc.InputsSchemaFile = ""
	}
	if doc.StateSchemaFile != "" {
		doc.StateSchema, err = loadRawSchema(nil, doc.ResolvedStateSchemaPath())
		if err != nil {
			return doc, err
		}
		doc.StateSchemaFile = ""
	}
	for i, layer := range doc.ExtendsLayers {
		doc.ExtendsLayers[i], err = freezeTaskSchemas(layer)
		if err != nil {
			return doc, err
		}
	}
	doc.Definition = nil
	return doc, nil
}

// RetiredRevisionSpellings reads the stored body rather than the decoded
// revision, because decoding drops a retired field without error and would
// hand the caller a revision that silently lost its environment. A body the
// decoder cannot read reports nothing here, so the decoder's error surfaces.
func RetiredRevisionSpellings(s *domain.Session) []string {
	if s == nil || s.WorkflowRevisionBody == "" {
		return nil
	}
	var body struct {
		Workflow map[string]json.RawMessage  `json:"workflow"`
		Tasks    map[string]retiredTaskShape `json:"tasks"`
		Provider json.RawMessage             `json:"provider"`
		Capture  json.RawMessage             `json:"workspace_capture_pending"`
	}
	if err := json.Unmarshal([]byte(s.WorkflowRevisionBody), &body); err != nil {
		return nil
	}
	found := map[string]bool{}
	if body.Provider != nil {
		found["@workflow"] = true
	}
	_, provider := body.Workflow["WorkspaceProvider"]
	_, providerInputs := body.Workflow["WorkspaceProviderInputs"]
	if provider || providerInputs || body.Capture != nil {
		found["workspace_provider"] = true
	}
	for _, doc := range body.Tasks {
		if doc.namesResourceObserver() {
			found["resource_observer"] = true
		}
	}
	if len(found) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(found))
}

// retiredTaskShape keys on presence, not value: a previous release encoded
// every task-document field, so even an empty observer name marks the dialect.
type retiredTaskShape struct {
	ResourceObserver *string            `json:"ResourceObserver"`
	ExtendsLayers    []retiredTaskShape `json:"ExtendsLayers"`
}

func (d retiredTaskShape) namesResourceObserver() bool {
	if d.ResourceObserver != nil {
		return true
	}
	return slices.ContainsFunc(d.ExtendsLayers, retiredTaskShape.namesResourceObserver)
}
