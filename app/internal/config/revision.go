package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

type WorkflowRevision struct {
	Workflow WorkflowFile                 `json:"workflow"`
	Effects  map[string]TaskDefinition    `json:"effects"`
	Tasks    map[string]TaskDocument      `json:"tasks"`
	Provider *WorkspaceProviderConfig     `json:"provider,omitempty"`
	Channels map[string]ChannelDefinition `json:"channels,omitempty"`
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
	if r.Provider != nil {
		provider := *r.Provider
		provider.BaseDir = ""
		if !provider.FromPlugin {
			provider.SourcePath = ""
		}
		r.Provider = &provider
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

func RevisionForSession(s *domain.Session) (WorkflowRevision, bool, error) {
	if s == nil || s.WorkflowRevision == "" {
		return WorkflowRevision{}, false, nil
	}
	r, err := DecodeWorkflowRevision([]byte(s.WorkflowRevisionBody), s.WorkflowRevision)
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
	return c.LoadTaskDefinitions(s.WorkspaceDirPath)
}

func (c *Config) TaskDeclarationsForSession(s *domain.Session) (map[string]TaskDocument, map[string]TaskDefinition, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		return r.Tasks, r.Effects, err
	}
	return c.LoadTaskDeclarations(s.WorkspaceDirPath)
}

func (c *Config) WorkspaceProviderForSession(s *domain.Session) (WorkspaceProviderConfig, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		if err != nil {
			return WorkspaceProviderConfig{}, err
		}
		if r.Provider == nil {
			return WorkspaceProviderConfig{}, fmt.Errorf("workflow %q has no workspace provider", s.Workflow)
		}
		return *r.Provider, nil
	}
	wf, err := c.ResolveSessionWorkflow(s)
	if err != nil {
		return WorkspaceProviderConfig{}, err
	}
	providers, err := c.LoadWorkspaceProviders()
	if err != nil {
		return WorkspaceProviderConfig{}, err
	}
	provider, ok := providers[wf.WorkspaceProvider]
	if !ok {
		return WorkspaceProviderConfig{}, fmt.Errorf("workflow %q: workspace provider %q not found", s.Workflow, wf.WorkspaceProvider)
	}
	return provider, nil
}

func (c *Config) ChannelsForSession(s *domain.Session) (map[string]ChannelDefinition, error) {
	if r, ok, err := RevisionForSession(s); ok || err != nil {
		return r.Channels, err
	}
	return c.LoadChannels()
}

func (c *Config) CaptureWorkflowRevision(workspaceDirPath, name string) (WorkflowRevision, error) {
	workflows, err := c.LoadWorkflows(workspaceDirPath)
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
	effects, err := c.LoadTaskDefinitions(workspaceDirPath)
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
	tasks, err := c.LoadTaskDocuments(workspaceDirPath)
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
	if wf.WorkspaceProvider != "" {
		providers, err := c.LoadWorkspaceProviders()
		if err != nil {
			return WorkflowRevision{}, err
		}
		provider, ok := providers[wf.WorkspaceProvider]
		if !ok {
			return WorkflowRevision{}, fmt.Errorf("workflow %q: workspace provider %q not found", name, wf.WorkspaceProvider)
		}
		if provider.InputsSchemaFile != "" {
			provider.InputsSchema, err = loadRawSchema(nil, provider.ResolvedInputsSchemaPath())
			if err != nil {
				return WorkflowRevision{}, err
			}
			provider.InputsSchemaFile = ""
		}
		if provider.OutputsSchemaFile != "" {
			provider.OutputsSchema, err = loadRawSchema(nil, provider.ResolvedOutputsSchemaPath())
			if err != nil {
				return WorkflowRevision{}, err
			}
			provider.OutputsSchemaFile = ""
		}
		revision.Provider = &provider
	}
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
	return revision, nil
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
