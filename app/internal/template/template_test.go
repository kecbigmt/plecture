package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_EmbeddedDefaults(t *testing.T) {
	modes := []string{"work", "review", "respond", "investigate"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			content, err := Load(mode, "")
			if err != nil {
				t.Fatalf("Load(%q) error: %v", mode, err)
			}
			if content == "" {
				t.Errorf("Load(%q) returned empty content", mode)
			}
		})
	}
}

func TestLoad_InvalidMode(t *testing.T) {
	_, err := Load("nonexistent", "")
	if err == nil {
		t.Error("Load(nonexistent) expected error, got nil")
	}
}

func TestLoad_UserGlobalOverride(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	templateDir := filepath.Join(tmpHome, ".config", "sennit", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	customContent := "Custom work template for {{.OwnerRepo}}"
	if err := os.WriteFile(filepath.Join(templateDir, "work.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", "")
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
	templateDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	customContent := "Repo-specific review template"
	if err := os.WriteFile(filepath.Join(templateDir, "review.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("review", repoDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != customContent {
		t.Errorf("Load() = %q, want %q", content, customContent)
	}
}

func TestRender(t *testing.T) {
	vars := Vars{
		URL:       "https://github.com/org/repo/issues/42",
		Number:    42,
		Repo:      "repo",
		OwnerRepo: "org/repo",
		Mode:      "work",
	}

	result, err := Render("work", "", vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(result, "org/repo#42") {
		t.Errorf("Render() result does not contain 'org/repo#42': %s", result)
	}
	if !strings.Contains(result, vars.URL) {
		t.Errorf("Render() result does not contain URL: %s", result)
	}
}

func TestRender_WithInstruction(t *testing.T) {
	vars := Vars{
		URL:         "https://github.com/org/repo/issues/1",
		Number:      1,
		Repo:        "repo",
		OwnerRepo:   "org/repo",
		Mode:        "work",
		Instruction: "Use Japanese comments",
	}

	result, err := Render("work", "", vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(result, "Use Japanese comments") {
		t.Errorf("Render() result does not contain instruction: %s", result)
	}
}

func TestRender_DefaultTemplates(t *testing.T) {
	modes := []string{"review", "respond", "work", "investigate"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			vars := Vars{
				URL:       "https://github.com/org/repo/issues/10",
				Number:    10,
				Repo:      "repo",
				OwnerRepo: "org/repo",
				Mode:      mode,
			}
			result, err := Render(mode, "", vars)
			if err != nil {
				t.Fatalf("Render(%q) error: %v", mode, err)
			}
			if result == "" {
				t.Errorf("Render(%q) returned empty result", mode)
			}
		})
	}
}

func TestRender_UnknownTemplate(t *testing.T) {
	vars := Vars{
		URL:       "https://github.com/org/repo/issues/1",
		Number:    1,
		Repo:      "repo",
		OwnerRepo: "org/repo",
		Mode:      "nonexistent",
	}
	_, err := Render("nonexistent", "", vars)
	if err == nil {
		t.Error("Render(nonexistent) expected error, got nil")
	}
}

func TestRender_VarsExpansion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	vars := Vars{
		URL:         "https://github.com/myorg/myrepo/pull/99",
		Number:      99,
		Repo:        "myrepo",
		OwnerRepo:   "myorg/myrepo",
		Mode:        "review",
		Instruction: "Focus on security",
	}

	result, err := Render("review", "", vars)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"URL", vars.URL},
		{"OwnerRepo#Number", "myorg/myrepo#99"},
	}
	for _, c := range checks {
		if !strings.Contains(result, c.want) {
			t.Errorf("Render() result missing %s (%q): %s", c.label, c.want, result)
		}
	}
}

func TestRender_SessionVarsNoURL(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Workdir overlay rooted at the session's working tree (not a GitHub repo).
	workdir := t.TempDir()
	sennitDir := filepath.Join(workdir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "owner={{.Workflow.outputs.owner}} session={{.SessionName}} " +
		"workdir={{.WorktreePath}} focus={{.SessionInputs.focus}} res={{.ResourceID}}"
	if err := os.WriteFile(filepath.Join(sennitDir, "kick.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{
		Mode:          "kick",
		SessionName:   "acme/_orchestrator",
		ResourceID:    "owner:acme",
		WorktreePath:  workdir,
		Workflow:      map[string]any{"owner": "acme"},
		SessionInputs: map[string]any{"focus": "triage"},
	}

	result, err := Render("kick", workdir, vars)
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
	sennitDir := filepath.Join(workdir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing Workflow output / session input must not error or panic; absent
	// keys render as Go's default "<no value>".
	if err := os.WriteFile(filepath.Join(sennitDir, "bare.md"), []byte("[{{.Workflow.outputs.missing}}]"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Render("bare", workdir, Vars{Mode: "bare"})
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
	sennitDir := filepath.Join(workdir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// `get` returns "" for a missing key instead of "<no value>".
	body := "[{{get .SessionInputs \"missing\"}}][{{get .SessionInputs \"focus\"}}]"
	if err := os.WriteFile(filepath.Join(sennitDir, "guard.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := Vars{Mode: "guard", SessionInputs: map[string]any{"focus": "triage"}}
	result, err := Render("guard", workdir, vars)
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
	t.Setenv("HOME", t.TempDir())

	meta, body, err := LoadWithMetadata("review", "")
	if err != nil {
		t.Fatalf("LoadWithMetadata() error: %v", err)
	}
	if meta.Description == "" {
		t.Error("expected non-empty description from embedded review template")
	}
	if strings.Contains(body, "---") {
		t.Error("body should not contain frontmatter delimiters")
	}
	if !strings.Contains(body, "{{.OwnerRepo}}") {
		t.Error("body should contain template variables")
	}
}

func TestRender_StripsFrontmatter(t *testing.T) {
	vars := Vars{
		URL:       "https://github.com/org/repo/pull/1",
		Number:    1,
		Repo:      "repo",
		OwnerRepo: "org/repo",
		Mode:      "review",
	}
	result, err := Render("review", "", vars)
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

func TestList_EmbeddedDefaults(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	templates, err := List("")
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
	sennitDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Repo-specific template that overrides embedded "work"
	repoContent := "---\ndescription: Custom work\n---\nBody"
	if err := os.WriteFile(filepath.Join(sennitDir, "work.md"), []byte(repoContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Repo-specific template that is unique
	customContent := "---\ndescription: Deploy to staging\n---\nDeploy"
	if err := os.WriteFile(filepath.Join(sennitDir, "deploy.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List(repoDir)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	names := make(map[string]string)
	for _, tmpl := range templates {
		names[tmpl.Name] = tmpl.Description
	}

	// "work" should use repo-specific description
	if names["work"] != "Custom work" {
		t.Errorf("work description = %q, want %q", names["work"], "Custom work")
	}

	// "deploy" should be present
	if names["deploy"] != "Deploy to staging" {
		t.Errorf("deploy description = %q, want %q", names["deploy"], "Deploy to staging")
	}

	// Embedded defaults should still be present
	if _, ok := names["review"]; !ok {
		t.Error("missing embedded template 'review'")
	}
}

func TestList_NoFrontmatter(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := t.TempDir()
	sennitDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Template without frontmatter
	if err := os.WriteFile(filepath.Join(sennitDir, "plain.md"), []byte("Just a template"), 0o644); err != nil {
		t.Fatal(err)
	}

	templates, err := List(repoDir)
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

// TestLoad_AncestorOverlay covers the bare layout: .sennit/templates lives in the
// repo container, one level above the branch worktree searchDir points at.
func TestLoad_AncestorOverlay(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := filepath.Join(tmpHome, "worktrees", "github.com", "org", "repo")
	sennitDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ancestorContent := "ANCESTOR REPO-LAYER TEMPLATE"
	if err := os.WriteFile(filepath.Join(sennitDir, "work.md"), []byte(ancestorContent), 0o644); err != nil {
		t.Fatal(err)
	}

	worktreeDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", worktreeDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != ancestorContent {
		t.Errorf("Load() = %q, want %q (ancestor repo-layer overlay should be found)", content, ancestorContent)
	}
}

// TestLoad_AncestorOverlay_InnerWins covers innermost-wins ordering when both
// the worktree itself and an ancestor declare the same template.
func TestLoad_AncestorOverlay_InnerWins(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoDir := filepath.Join(tmpHome, "worktrees", "github.com", "org", "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".sennit", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	outerContent := "OUTER (REPO CONTAINER) TEMPLATE"
	if err := os.WriteFile(filepath.Join(repoDir, ".sennit", "templates", "work.md"), []byte(outerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	worktreeDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(filepath.Join(worktreeDir, ".sennit", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	innerContent := "INNER (WORKTREE) TEMPLATE"
	if err := os.WriteFile(filepath.Join(worktreeDir, ".sennit", "templates", "work.md"), []byte(innerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := Load("work", worktreeDir)
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

	repoDir := filepath.Join(tmpHome, "worktrees", "github.com", "org", "repo")
	sennitDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customContent := "---\ndescription: Ancestor deploy\n---\nDeploy"
	if err := os.WriteFile(filepath.Join(sennitDir, "deploy.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	worktreeDir := filepath.Join(repoDir, "session")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	templates, err := List(worktreeDir)
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
	sennitDir := filepath.Join(repoDir, ".sennit", "templates")
	if err := os.MkdirAll(sennitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoContent := "REPO SPECIFIC TEMPLATE"
	if err := os.WriteFile(filepath.Join(sennitDir, "work.md"), []byte(repoContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also create a user-global template
	userDir := filepath.Join(tmpHome, ".config", "sennit", "templates")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := "USER GLOBAL TEMPLATE"
	if err := os.WriteFile(filepath.Join(userDir, "work.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Repo-specific should win over user-global and embedded
	content, err := Load("work", repoDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != repoContent {
		t.Errorf("Load() = %q, want %q (repo-specific should take priority)", content, repoContent)
	}

	// Without repoDir, user-global should win
	content, err = Load("work", "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if content != userContent {
		t.Errorf("Load() = %q, want %q (user-global should take priority over embedded)", content, userContent)
	}
}
