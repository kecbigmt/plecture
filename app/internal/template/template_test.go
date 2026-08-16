package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/confighome"
)

func TestLoad_MissingTemplateNamesSearchedPaths(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workdir := filepath.Join(tmpHome, "projects", "repo", "session")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Load("review", workdir, nil)
	if err == nil {
		t.Fatal("Load() expected a missing-template error")
	}
	msg := err.Error()
	for _, want := range []string{
		`no template found for mode "review"`,
		filepath.Join(workdir, ".plect", "templates", "review.md"),
		filepath.Join(tmpHome, ".config", "plect", "templates", "review.md"),
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not name searched path %q", msg, want)
		}
	}
	if strings.Contains(msg, "embedded") || strings.Contains(msg, "defaults") {
		t.Fatalf("error %q mentions an embedded fallback", msg)
	}
}

func TestLoad_InvalidMode(t *testing.T) {
	_, err := Load("nonexistent", "", nil)
	if err == nil {
		t.Error("Load(nonexistent) expected error, got nil")
	}
}

func TestLoad_UserGlobalOverride(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	customContent := "Custom work template for {{.SessionName}}"
	if err := os.WriteFile(filepath.Join(templateDir, "work.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", "", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != customContent {
		t.Errorf("Load() = %q, want %q", content, customContent)
	}
}

func TestLoad_UserGlobalOverride_HonorsConfigHomeEnvVar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configHome := t.TempDir()
	t.Setenv(confighome.EnvVar, configHome)

	templateDir := filepath.Join(configHome, "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customContent := "Custom work template under PLECT_CONFIG_HOME"
	if err := os.WriteFile(filepath.Join(templateDir, "work.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", "", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != customContent {
		t.Errorf("Load() = %q, want %q", content, customContent)
	}
}

func TestLoad_RepoSpecificOverride(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := t.TempDir()
	templateDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	customContent := "Repo-specific review template"
	if err := os.WriteFile(filepath.Join(templateDir, "review.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("review", repoDir, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != customContent {
		t.Errorf("Load() = %q, want %q", content, customContent)
	}
}

func TestRender(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{{.Workflow.outputs.owner_repo}}#{{.Workflow.outputs.number}}
{{.ResourceID}}`
	if err := os.WriteFile(filepath.Join(templateDir, "work.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Workflow:   map[string]any{"owner_repo": "org/repo", "number": 42},
		ResourceID: "https://example.test/org/repo/items/42",
		Mode:       "work",
	}

	result, err := Render("work", "", nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(result, "org/repo#42") {
		t.Errorf("Render() result does not contain 'org/repo#42': %s", result)
	}
	if !strings.Contains(result, vars.ResourceID) {
		t.Errorf("Render() result does not contain the resource id: %s", result)
	}
}

func TestRender_WithInstruction(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "work.md"), []byte(`{{.Instruction}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Workflow:    map[string]any{"owner_repo": "org/repo", "number": 42},
		ResourceID:  "https://example.test/org/repo/items/42",
		Mode:        "work",
		Instruction: "Use Japanese comments",
	}

	result, err := Render("work", "", nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(result, "Use Japanese comments") {
		t.Errorf("Render() result does not contain instruction: %s", result)
	}
}

func TestRender_UnknownTemplate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	vars := Vars{
		Workflow:   map[string]any{"owner_repo": "org/repo", "number": 42},
		ResourceID: "https://example.test/org/repo/items/42",
		Mode:       "nonexistent",
	}
	_, err := Render("nonexistent", "", nil, vars)
	if err == nil {
		t.Error("Render(nonexistent) expected error, got nil")
	}
}

func TestRender_VarsExpansion(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{{.Workflow.outputs.owner_repo}}#{{.Workflow.outputs.number}}
{{.ResourceID}}`
	if err := os.WriteFile(filepath.Join(templateDir, "review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Workflow:    map[string]any{"owner_repo": "myorg/myrepo", "number": 99},
		ResourceID:  "https://example.test/myorg/myrepo/items/99",
		Mode:        "review",
		Instruction: "Focus on security",
	}

	result, err := Render("review", "", nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"resource id", vars.ResourceID},
		{"repository and number", "myorg/myrepo#99"},
	}
	for _, c := range checks {
		if !strings.Contains(result, c.want) {
			t.Errorf("Render() result missing %s (%q): %s", c.label, c.want, result)
		}
	}
}

func TestRender_SessionVarsWithoutProviderOutputs(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Workdir overlay rooted at the session's working tree, not a repository.
	workdir := t.TempDir()
	plectDir := filepath.Join(workdir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "owner={{.Workflow.outputs.owner}} session={{.SessionName}} " +
		"workdir={{.WorkdirPath}} focus={{.SessionInputs.focus}} res={{.ResourceID}}"
	if err := os.WriteFile(filepath.Join(plectDir, "kick.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Mode:          "kick",
		SessionName:   "acme/_orchestrator",
		ResourceID:    "owner:acme",
		WorkdirPath:   workdir,
		Workflow:      map[string]any{"owner": "acme"},
		SessionInputs: map[string]any{"focus": "triage"},
	}

	result, err := Render("kick", workdir, nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	want := "owner=acme session=acme/_orchestrator workdir=" + workdir +
		" focus=triage res=owner:acme"
	if result != want {
		t.Errorf("Render() = %q, want %q", result, want)
	}
}

func TestRender_EmptyVars(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workdir := t.TempDir()
	plectDir := filepath.Join(workdir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing Workflow output / session input must not error or panic; absent
	// keys render as Go's default "<no value>".
	if err := os.WriteFile(filepath.Join(plectDir, "bare.md"), []byte("[{{.Workflow.outputs.missing}}]"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Render("bare", workdir, nil, Vars{Mode: "bare"})
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if result != "[<no value>]" {
		t.Errorf("Render() = %q, want %q", result, "[<no value>]")
	}
}

func TestRender_GetGuardsOptionalVars(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workdir := t.TempDir()
	plectDir := filepath.Join(workdir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// `get` returns "" for a missing key instead of "<no value>".
	body := "[{{get .SessionInputs \"missing\"}}][{{get .SessionInputs \"focus\"}}]"
	if err := os.WriteFile(filepath.Join(plectDir, "guard.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{Mode: "guard", SessionInputs: map[string]any{"focus": "triage"}}
	result, err := Render("guard", workdir, nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if result != "[][triage]" {
		t.Errorf("Render() = %q, want %q", result, "[][triage]")
	}
}

func TestParseFrontmatter(t *testing.T) {
	t.Run("with frontmatter", func(t *testing.T) {
		input := "---\ndescription: Test description\n---\nBody content"
		meta, body := parseFrontmatter(input)
		if meta.Description != "Test description" {
			t.Errorf("Description = %q, want %q", meta.Description, "Test description")
		}
		if body != "Body content" {
			t.Errorf("body = %q, want %q", body, "Body content")
		}
	})

	t.Run("without frontmatter", func(t *testing.T) {
		input := "Just plain content"
		meta, body := parseFrontmatter(input)
		if meta.Description != "" {
			t.Errorf("Description = %q, want empty", meta.Description)
		}
		if body != input {
			t.Errorf("body = %q, want %q", body, input)
		}
	})

	t.Run("unknown keys ignored", func(t *testing.T) {
		input := "---\nunknown: value\ndescription: kept\n---\nBody"
		meta, body := parseFrontmatter(input)
		if meta.Description != "kept" {
			t.Errorf("Description = %q, want %q", meta.Description, "kept")
		}
		if body != "Body" {
			t.Errorf("body = %q, want %q", body, "Body")
		}
	})

	t.Run("unclosed frontmatter treated as no frontmatter", func(t *testing.T) {
		input := "---\ndescription: value\nno closing delimiter"
		meta, body := parseFrontmatter(input)
		if meta.Description != "" {
			t.Errorf("Description = %q, want empty", meta.Description)
		}
		if body != input {
			t.Errorf("body = %q, want %q", body, input)
		}
	})
}

func TestLoadWithMetadata(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: Review changes\n---\nReview {{.Workflow.outputs.owner_repo}}"
	if err := os.WriteFile(filepath.Join(templateDir, "review.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, body, err := LoadWithMetadata("review", "", nil)
	if err != nil {
		t.Fatalf("LoadWithMetadata() error: %v", err)
	}
	if meta.Description == "" {
		t.Error("expected non-empty description from user review template")
	}
	if strings.Contains(body, "---") {
		t.Error("body should not contain frontmatter delimiters")
	}
	if !strings.Contains(body, "{{.Workflow.outputs.owner_repo}}") {
		t.Error("body should contain template variables")
	}
}

func TestRender_StripsFrontmatter(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: Review changes\n---\nReview {{.Workflow.outputs.owner_repo}}"
	if err := os.WriteFile(filepath.Join(templateDir, "review.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Workflow:   map[string]any{"owner_repo": "org/repo", "number": 42},
		ResourceID: "https://example.test/org/repo/items/42",
		Mode:       "review",
	}
	result, err := Render("review", "", nil, vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if strings.Contains(result, "---") {
		t.Error("rendered output should not contain frontmatter delimiters")
	}
	if strings.Contains(result, "description:") {
		t.Error("rendered output should not contain frontmatter fields")
	}
}

func TestList_UserGlobalTemplates(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	templateDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, desc := range map[string]string{
		"work":        "Work on the task",
		"review":      "Review changes",
		"respond":     "Respond to an event",
		"investigate": "Investigate context",
	} {
		content := "---\ndescription: " + desc + "\n---\nBody"
		if err := os.WriteFile(filepath.Join(templateDir, name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	templates, err := List("", nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(templates) != 4 {
		t.Fatalf("List() returned %d templates, want 4", len(templates))
	}

	names := make(map[string]string)
	for _, tmpl := range templates {
		names[tmpl.Name] = tmpl.Description
	}

	for _, name := range []string{"work", "review", "respond", "investigate"} {
		desc, ok := names[name]
		if !ok {
			t.Errorf("missing template %q", name)
		}
		if desc == "" {
			t.Errorf("template %q has empty description", name)
		}
	}
}

func TestList_RepoSpecificAndDedup(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := t.TempDir()
	plectDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Workdir-specific template that overrides user-global "work".
	repoContent := "---\ndescription: Custom work\n---\nBody"
	if err := os.WriteFile(filepath.Join(plectDir, "work.md"), []byte(repoContent), 0o644); err != nil {
		t.Fatal(err)
	}
	userDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "review.md"), []byte("---\ndescription: User review\n---\nReview"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Repo-specific template that is unique
	customContent := "---\ndescription: Deploy to staging\n---\nDeploy"
	if err := os.WriteFile(filepath.Join(plectDir, "deploy.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List(repoDir, nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	names := make(map[string]string)
	for _, tmpl := range templates {
		names[tmpl.Name] = tmpl.Description
	}

	// "work" should use workdir-specific description.
	if names["work"] != "Custom work" {
		t.Errorf("work description = %q, want %q", names["work"], "Custom work")
	}

	// "deploy" should be present
	if names["deploy"] != "Deploy to staging" {
		t.Errorf("deploy description = %q, want %q", names["deploy"], "Deploy to staging")
	}

	// User-global templates should still be present.
	if _, ok := names["review"]; !ok {
		t.Error("missing user-global template 'review'")
	}
}

func TestList_NoFrontmatter(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := t.TempDir()
	plectDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Template without frontmatter
	if err := os.WriteFile(filepath.Join(plectDir, "plain.md"), []byte("Just a template"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List(repoDir, nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	names := make(map[string]string)
	for _, tmpl := range templates {
		names[tmpl.Name] = tmpl.Description
	}

	if desc, ok := names["plain"]; !ok {
		t.Error("missing template 'plain'")
	} else if desc != "" {
		t.Errorf("plain description = %q, want empty", desc)
	}
}

// TestLoad_AncestorOverlay covers the bare layout: .plect/templates lives in the
// repo container, one level above the branch workdir searchDir points at.
func TestLoad_AncestorOverlay(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := filepath.Join(tmpHome, "workdirs", "github.com", "org", "repo")
	plectDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ancestorContent := "ANCESTOR REPO-LAYER TEMPLATE"
	if err := os.WriteFile(filepath.Join(plectDir, "work.md"), []byte(ancestorContent), 0o644); err != nil {
		t.Fatal(err)
	}

	workdirDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workdirDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", workdirDir, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != ancestorContent {
		t.Errorf("Load() = %q, want %q (ancestor repo-layer overlay should be found)", content, ancestorContent)
	}
}

// TestLoad_AncestorOverlay_InnerWins covers innermost-wins ordering when both
// the workdir itself and an ancestor declare the same template.
func TestLoad_AncestorOverlay_InnerWins(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := filepath.Join(tmpHome, "workdirs", "github.com", "org", "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".plect", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	outerContent := "OUTER (REPO CONTAINER) TEMPLATE"
	if err := os.WriteFile(filepath.Join(repoDir, ".plect", "templates", "work.md"), []byte(outerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	workdirDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(filepath.Join(workdirDir, ".plect", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	innerContent := "INNER (WORKTREE) TEMPLATE"
	if err := os.WriteFile(filepath.Join(workdirDir, ".plect", "templates", "work.md"), []byte(innerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", workdirDir, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != innerContent {
		t.Errorf("Load() = %q, want %q (innermost overlay should win)", content, innerContent)
	}
}

// TestList_AncestorOverlay covers the same ancestor walk for List().
func TestList_AncestorOverlay(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := filepath.Join(tmpHome, "workdirs", "github.com", "org", "repo")
	plectDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customContent := "---\ndescription: Ancestor deploy\n---\nDeploy"
	if err := os.WriteFile(filepath.Join(plectDir, "deploy.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	workdirDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(workdirDir, 0o755); err != nil {
		t.Fatal(err)
	}

	templates, err := List(workdirDir, nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	names := make(map[string]string)
	for _, tmpl := range templates {
		names[tmpl.Name] = tmpl.Description
	}
	if names["deploy"] != "Ancestor deploy" {
		t.Errorf("deploy description = %q, want %q", names["deploy"], "Ancestor deploy")
	}
}

func TestLoad_Priority(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := t.TempDir()
	plectDir := filepath.Join(repoDir, ".plect", "templates")
	if err := os.MkdirAll(plectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoContent := "REPO SPECIFIC TEMPLATE"
	if err := os.WriteFile(filepath.Join(plectDir, "work.md"), []byte(repoContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also create a user-global template
	userDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := "USER GLOBAL TEMPLATE"
	if err := os.WriteFile(filepath.Join(userDir, "work.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Repo-specific should win over user-global and embedded
	content, err := Load("work", repoDir, nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != repoContent {
		t.Errorf("Load() = %q, want %q (repo-specific should take priority)", content, repoContent)
	}

	// Without repoDir, user-global should win
	content, err = Load("work", "", nil)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != userContent {
		t.Errorf("Load() = %q, want %q (user-global should take priority over embedded)", content, userContent)
	}
}

func TestLoad_PluginLayerIsLowestPriority(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pluginDir := t.TempDir()
	pluginContent := "Plugin work template"
	if err := os.MkdirAll(filepath.Join(pluginDir, "config", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "config", "templates", "work.md"), []byte(pluginContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// No workdir or user-global override: the plugin layer is found.
	content, err := Load("work", "", []string{pluginDir})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != pluginContent {
		t.Errorf("Load() = %q, want %q (plugin layer)", content, pluginContent)
	}

	// A user-global template shadows the plugin's.
	userDir := filepath.Join(tmpHome, ".config", "plect", "templates")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := "User work template"
	if err := os.WriteFile(filepath.Join(userDir, "work.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err = Load("work", "", []string{pluginDir})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != userContent {
		t.Errorf("Load() = %q, want %q (user-global should shadow the plugin layer)", content, userContent)
	}
}

func TestLoad_TwoPluginLayersSameModeFailsLoud(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pluginA := t.TempDir()
	pluginB := t.TempDir()
	for _, dir := range []string{pluginA, pluginB} {
		if err := os.MkdirAll(filepath.Join(dir, "config", "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config", "templates", "review.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Load("review", "", []string{pluginA, pluginB}); err == nil {
		t.Fatal("Load() expected a same-id-across-plugin-layers error, got nil")
	}
}

func TestList_PluginLayerAppendsBelowUserGlobal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pluginDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginDir, "config", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "config", "templates", "kick.md"), []byte("---\ndescription: Plugin kick\n---\nBody"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List("", []string{pluginDir})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "kick" {
		t.Fatalf("List() = %+v, want a single \"kick\" entry", templates)
	}
}

func TestList_TwoPluginLayersSameNameFailsLoud(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pluginA := t.TempDir()
	pluginB := t.TempDir()
	for _, dir := range []string{pluginA, pluginB} {
		if err := os.MkdirAll(filepath.Join(dir, "config", "templates"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config", "templates", "kick.md"), []byte("Body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := List("", []string{pluginA, pluginB}); err == nil {
		t.Fatal("List() expected a same-id-across-plugin-layers error, got nil")
	}
}
