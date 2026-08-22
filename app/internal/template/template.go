package template

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/kecbigmt/plecture/app/internal/confighome"
)

// templateFuncs mirrors the task render context so optional vars can be
// guarded the same way in templates: `{{get .SessionInputs "focus" ""}}`
// yields the third argument when the key is absent instead of injecting
// "<no value>".
var templateFuncs = template.FuncMap{
	// A present-but-empty value is a value and does not take the default;
	// a nil one does, because rendering it would emit the "<no value>"
	// this helper exists to avoid.
	"get": func(m map[string]any, key string, def any) any {
		if m == nil {
			return def
		}
		if v, ok := m[key]; ok && v != nil {
			return v
		}
		return def
	},
}

// Vars is the variable bundle for templates. The session-derived fields
// mirror the task render context (SessionName / ResourceID /
// WorkspaceDirPath / Workflow outputs / SessionInputs) so a template
// authored for a task reads the same way here. Anything resource-shaped a
// template needs beyond the resource id comes from the workspace provider's
// setup outputs, exposed as .Workflow.outputs.<key>.
type Vars struct {
	Mode        string
	Instruction string

	// Session-derived (workspace-provider-agnostic).
	SessionName      string
	ResourceID       string
	WorkspaceDirPath string
	Workflow         map[string]any // workspace provider setup outputs, exposed as .Workflow.outputs.<key>
	SessionInputs    map[string]any // session inputs + explicit --var, exposed as .SessionInputs.<key>
	Inputs           map[string]any // the declaring definition's own bound inputs, exposed as .Inputs.<key>
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

// LoadWithMetadata reads a template and returns its metadata and raw content
// separately. pluginDirs are resolved plugin roots (Config.PluginDirs); see
// Load.
func LoadWithMetadata(mode, searchDir string, pluginDirs []string) (Metadata, string, error) {
	raw, err := Load(mode, searchDir, pluginDirs)
	if err != nil {
		return Metadata{}, "", err
	}
	meta, body := parseFrontmatter(raw)
	return meta, body, nil
}

// Load reads a template for the given mode with the following priority:
//  1. <searchDir>/.plect/templates/<mode>.md and its ancestors, innermost wins
//  2. <config home>/templates/<mode>.md (user global; ~/.config/plect by
//     default, see confighome.Resolve)
//  3. <pluginDir>/templates/<mode>.md, for each resolved plugin (read-only
//     base layer; see pluginTemplateFile for its same-id conflict rule)
//
// searchDir is the session's workdir, not a repository path: the overlay is
// rooted at the working tree so it works for any provider.
func Load(mode, searchDir string, pluginDirs []string) (string, error) {
	filename := mode + ".md"
	var searched []string

	// 1. Working-directory overlay, walked up through ancestors
	for _, dir := range ancestorDirs(searchDir) {
		repoPath := filepath.Join(dir, ".plect", "templates", filename)
		searched = append(searched, repoPath)
		if data, err := os.ReadFile(repoPath); err == nil {
			return string(data), nil
		}
	}

	// 2. User global template
	configHome, _ := confighome.Resolve()
	userPath := filepath.Join(configHome, "templates", filename)
	searched = append(searched, userPath)
	if data, err := os.ReadFile(userPath); err == nil {
		return string(data), nil
	}

	// 3. Plugin base layer
	pluginPath, err := pluginTemplateFile(mode, pluginDirs)
	if err != nil {
		return "", err
	}
	if pluginPath != "" {
		searched = append(searched, pluginPath)
		if data, err := os.ReadFile(pluginPath); err == nil {
			return string(data), nil
		}
	}

	return "", fmt.Errorf("no template found for mode %q; searched: %s", mode, strings.Join(searched, ", "))
}

// pluginTemplateFile returns the `templates/<mode>.md` path from exactly one
// of pluginDirs. Two different plugin dirs both declaring the same mode is a
// same-id conflict between plugin layers — a load error per the
// plugin-packaging design's Templates row — rather than declaration order
// silently picking one.
func pluginTemplateFile(mode string, pluginDirs []string) (string, error) {
	filename := mode + ".md"
	var found []string
	for _, dir := range pluginDirs {
		p := filepath.Join(dir, "config", "templates", filename)
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("template %q is defined by more than one plugin layer (%s); replace one definition in global config to resolve the conflict", mode, strings.Join(found, ", "))
	}
}

// ancestorDirs walks up from searchDir, innermost first, so the closest
// override wins. $HOME is the exclusive upper bound because the user's
// global config lives at ~/.config/plect/ and ~/.plect/ would collide with it.
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
// the session's working directory whose `.plect/templates/` overlay is consulted
// first. Frontmatter is stripped
// before rendering.
//
// Workflow outputs are exposed as `.Workflow.outputs.<key>` and session inputs
// as `.SessionInputs.<key>`, matching the task render context.
func Render(mode, searchDir string, pluginDirs []string, vars Vars) (string, error) {
	_, body, err := LoadWithMetadata(mode, searchDir, pluginDirs)
	if err != nil {
		return "", err
	}
	return RenderBody(mode, body, vars)
}

// RenderBody renders a body the caller already holds — a task document's
// instruction, which carries the conditional and defaulting forms the
// instruction assets already had. Retirement: the decision on control flow in
// the instruction body replaces this pass with the language's own construct.
func RenderBody(mode, body string, vars Vars) (string, error) {
	tmpl, err := template.New(mode).Funcs(templateFuncs).Parse(body)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", mode, err)
	}

	outputs := vars.Workflow
	if outputs == nil {
		outputs = map[string]any{}
	}
	sessionInputs := vars.SessionInputs
	if sessionInputs == nil {
		sessionInputs = map[string]any{}
	}
	inputs := vars.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	data := struct {
		Mode             string
		Instruction      string
		SessionName      string
		ResourceID       string
		WorkspaceDirPath string
		Workflow         map[string]any
		SessionInputs    map[string]any
		Inputs           map[string]any
	}{
		Mode:             vars.Mode,
		Instruction:      vars.Instruction,
		SessionName:      vars.SessionName,
		ResourceID:       vars.ResourceID,
		WorkspaceDirPath: vars.WorkspaceDirPath,
		Workflow:         map[string]any{"outputs": outputs},
		SessionInputs:    sessionInputs,
		Inputs:           inputs,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %q: %w", mode, err)
	}

	return buf.String(), nil
}

// List returns all available templates with metadata for the given workdir.
// Templates are deduplicated by name with workdir-specific > user-global >
// plugin priority. pluginDirs are resolved plugin roots (Config.PluginDirs);
// two different plugin dirs declaring the same template name is a load
// error, mirroring Load's pluginTemplateFile rule.
func List(repoDir string, pluginDirs []string) ([]TemplateInfo, error) {
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

	// 1. Workdir-specific, walked up through ancestors.
	for _, anc := range ancestorDirs(repoDir) {
		dir := filepath.Join(anc, ".plect", "templates")
		addFromDir(
			func() ([]fs.DirEntry, error) { return os.ReadDir(dir) },
			func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) },
		)
	}

	// 2. User global
	configHome, _ := confighome.Resolve()
	userDir := filepath.Join(configHome, "templates")
	addFromDir(
		func() ([]fs.DirEntry, error) { return os.ReadDir(userDir) },
		func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(userDir, name)) },
	)

	// 3. Plugin base layer. Same-id conflicts between plugin dirs are
	// checked up front, before any workdir/global shadowing is applied —
	// declaration order must never silently pick one plugin's template over
	// another's.
	pluginOwner := make(map[string]string)
	for _, dir := range pluginDirs {
		templatesDir := filepath.Join(dir, "config", "templates")
		entries, err := os.ReadDir(templatesDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".md")
			if owner, exists := pluginOwner[name]; exists {
				return nil, fmt.Errorf("template %q is defined by more than one plugin layer (%s and %s); replace one definition in global config to resolve the conflict", name, owner, dir)
			}
			pluginOwner[name] = dir
		}
	}
	for _, dir := range pluginDirs {
		templatesDir := filepath.Join(dir, "config", "templates")
		addFromDir(
			func() ([]fs.DirEntry, error) { return os.ReadDir(templatesDir) },
			func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(templatesDir, name)) },
		)
	}

	return result, nil
}
