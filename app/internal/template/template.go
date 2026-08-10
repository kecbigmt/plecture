package template

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed defaults
var DefaultTemplates embed.FS

// templateFuncs mirrors the task render context so optional vars can be
// guarded the same way in prompt templates: `{{get .SessionInputs "focus"}}`
// returns "" when the key is absent instead of injecting "<no value>".
var templateFuncs = template.FuncMap{
	"get": func(m map[string]any, key string) any {
		if m == nil {
			return ""
		}
		if v, ok := m[key]; ok && v != nil {
			return v
		}
		return ""
	},
}

// Vars is the variable bundle for prompt templates. The session-derived fields
// mirror the task render context (SessionName / ResourceID / WorktreePath /
// Workflow outputs / SessionInputs) so a template authored for a task reads
// the same way here. Anything resource-shaped a template needs beyond the
// resource id comes from the provider's setup outputs, exposed as
// .Workflow.outputs.<key>.
type Vars struct {
	Mode        string
	Instruction string

	// Session-derived (provider-agnostic).
	SessionName   string
	ResourceID    string
	WorktreePath  string
	Workflow      map[string]any // provider setup outputs, exposed as .Workflow.outputs.<key>
	SessionInputs map[string]any // session inputs + explicit --var, exposed as .SessionInputs.<key>
}

// Metadata holds frontmatter fields parsed from a template.
type Metadata struct {
	Description string `json:"description"`
}

// TemplateInfo holds a template name and its metadata.
type TemplateInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// parseFrontmatter splits a template into metadata and body.
// If no frontmatter is present, metadata fields are empty and body is the full content.
func parseFrontmatter(content string) (Metadata, string) {
	if !strings.HasPrefix(content, "---\n") {
		return Metadata{}, content
	}

	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return Metadata{}, content
	}

	fm := content[4 : 4+end]
	body := content[4+end+5:] // skip past "\n---\n"

	var meta Metadata
	for _, line := range strings.Split(fm, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "description":
			meta.Description = value
		}
	}

	return meta, body
}

// LoadWithMetadata reads a template and returns its metadata and raw content separately.
func LoadWithMetadata(mode, searchDir string) (Metadata, string, error) {
	raw, err := Load(mode, searchDir)
	if err != nil {
		return Metadata{}, "", err
	}
	meta, body := parseFrontmatter(raw)
	return meta, body, nil
}

// Load reads a template for the given mode with the following priority:
//  1. <searchDir>/.sennit/templates/<mode>.md and its ancestors, innermost wins
//     (bare layout: .sennit lives in the repo container, one level above the
//     branch worktree)
//  2. ~/.config/sennit/templates/<mode>.md (user global)
//  3. embedded defaults
//
// searchDir is the session's workdir, not a repository path: the overlay is
// rooted at the working tree so it works for any provider.
func Load(mode, searchDir string) (string, error) {
	filename := mode + ".md"

	// 1. Working-directory overlay, walked up through ancestors
	for _, dir := range ancestorDirs(searchDir) {
		repoPath := filepath.Join(dir, ".sennit", "templates", filename)
		if data, err := os.ReadFile(repoPath); err == nil {
			return string(data), nil
		}
	}

	// 2. User global template
	home, _ := os.UserHomeDir()
	userPath := filepath.Join(home, ".config", "sennit", "templates", filename)
	if data, err := os.ReadFile(userPath); err == nil {
		return string(data), nil
	}

	// 3. Embedded default
	data, err := DefaultTemplates.ReadFile("defaults/" + filename)
	if err != nil {
		return "", fmt.Errorf("no template found for mode %q", mode)
	}
	return string(data), nil
}

// ancestorDirs walks up from searchDir, innermost first, so the closest
// override wins. $HOME is the exclusive upper bound because the user's
// global config lives at ~/.config/sennit/ and ~/.sennit/ would collide with it.
// Mirrors config.cascadeAncestors (outermost-first, for layer merging); this
// package needs the reverse order for first-match-wins lookup.
func ancestorDirs(searchDir string) []string {
	if searchDir == "" {
		return nil
	}
	var cleanHome string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome = filepath.Clean(home)
	}
	var dirs []string
	cur := filepath.Clean(searchDir)
	for {
		if cur == cleanHome || cur == string(filepath.Separator) {
			break
		}
		dirs = append(dirs, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return dirs
}

// Render loads and renders a template with the given variables. searchDir is
// the session's working directory whose `.sennit/templates/` overlay is consulted
// first (empty falls back to user-global + embedded). Frontmatter is stripped
// before rendering.
//
// Workflow outputs are exposed as `.Workflow.outputs.<key>` and session inputs
// as `.SessionInputs.<key>`, matching the task render context.
func Render(mode, searchDir string, vars Vars) (string, error) {
	_, body, err := LoadWithMetadata(mode, searchDir)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New(mode).Funcs(templateFuncs).Parse(body)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", mode, err)
	}

	outputs := vars.Workflow
	if outputs == nil {
		outputs = map[string]any{}
	}
	inputs := vars.SessionInputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	data := struct {
		Mode          string
		Instruction   string
		SessionName   string
		ResourceID    string
		WorktreePath  string
		Workflow      map[string]any
		SessionInputs map[string]any
	}{
		Mode:          vars.Mode,
		Instruction:   vars.Instruction,
		SessionName:   vars.SessionName,
		ResourceID:    vars.ResourceID,
		WorktreePath:  vars.WorktreePath,
		Workflow:      map[string]any{"outputs": outputs},
		SessionInputs: inputs,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %q: %w", mode, err)
	}

	return buf.String(), nil
}

// List returns all available templates with metadata for the given repo.
// Templates are deduplicated by name with repo-specific > user-global > embedded priority.
func List(repoDir string) ([]TemplateInfo, error) {
	seen := make(map[string]bool)
	var result []TemplateInfo

	addFromDir := func(readDir func() ([]fs.DirEntry, error), loadRaw func(string) ([]byte, error)) {
		entries, err := readDir()
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".md")
			if seen[name] {
				continue
			}
			seen[name] = true
			data, err := loadRaw(entry.Name())
			if err != nil {
				continue
			}
			meta, _ := parseFrontmatter(string(data))
			result = append(result, TemplateInfo{
				Name:        name,
				Description: meta.Description,
			})
		}
	}

	// 1. Repo-specific, walked up through ancestors (innermost wins via seen-dedup)
	for _, anc := range ancestorDirs(repoDir) {
		dir := filepath.Join(anc, ".sennit", "templates")
		addFromDir(
			func() ([]fs.DirEntry, error) { return os.ReadDir(dir) },
			func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) },
		)
	}

	// 2. User global
	home, _ := os.UserHomeDir()
	userDir := filepath.Join(home, ".config", "sennit", "templates")
	addFromDir(
		func() ([]fs.DirEntry, error) { return os.ReadDir(userDir) },
		func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(userDir, name)) },
	)

	// 3. Embedded defaults
	addFromDir(
		func() ([]fs.DirEntry, error) { return fs.ReadDir(DefaultTemplates, "defaults") },
		func(name string) ([]byte, error) { return DefaultTemplates.ReadFile("defaults/" + name) },
	)

	return result, nil
}
