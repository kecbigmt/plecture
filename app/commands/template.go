package commands

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/github"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/template"
	"github.com/kecbigmt/plect/app/internal/workspace"
	contractstate "github.com/kecbigmt/plect/contracts/state"
)

var (
	templateRenderSession     string
	templateRenderURL         string
	templateRenderResource    string
	templateRenderRepo        string
	templateRenderInstruction string
	templateRenderVars        []string
	templateListRepo          string
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage prompt templates",
}

var templateRenderCmd = &cobra.Command{
	Use:   "render <template-name>",
	Short: "Render a prompt template to stdout",
	Long: `Render a prompt template with the given variables and print to stdout.

Variables come from a session (provider-agnostic) or, for GitHub workflows, a
legacy --url. Pass --session <identifier> (session name, alias, or resource id)
to resolve a session and expose its vars to the template:
  {{.SessionName}} {{.ResourceID}} {{.WorktreePath}}
  {{.Workflow.outputs.<key>}}  — provider setup outputs (workdir, owner, title, ...)
  {{.SessionInputs.<key>}}     — session inputs and explicit --var values
  {{.URL}} {{.Number}} {{.Repo}} {{.OwnerRepo}}  — GitHub-shaped sessions only

Guard optional vars with {{get .SessionInputs "key"}} (returns "" when absent)
instead of {{.SessionInputs.key}} (renders "<no value>").

The template name corresponds to files in the template search path:
  1. <workdir>/.tws/templates/<name>.md  (session working-directory overlay)
  2. ~/.config/tws/templates/<name>.md
  3. Built-in defaults (review, respond, work, investigate)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templateName := args[0]
		cfg := config.Load()

		extra, err := parseTemplateVars(templateRenderVars)
		if err != nil {
			return err
		}

		var vars template.Vars
		searchDir := ""
		resolved := false

		// A session identifier (or a URL that resolves to a tracked session)
		// yields provider-agnostic vars and roots the overlay at the workdir.
		ident := templateRenderSession
		if ident == "" {
			ident = templateRenderURL
		}
		if ident != "" {
			store := state.NewStore("")
			if name, sess, rerr := service.ResolveSession(cfg, store, ident); rerr == nil {
				vars = templateVarsFromSession(name, sess)
				searchDir = sess.WorktreePath
				resolved = true
			} else if templateRenderSession != "" {
				return rerr
			}
		}

		// Legacy GitHub path: a --url with no tracked session derives vars from
		// the URL and roots the overlay at the repo directory.
		if !resolved && templateRenderURL != "" {
			parsed, perr := github.ParseURL(templateRenderURL)
			if perr != nil {
				return perr
			}
			vars.URL = templateRenderURL
			vars.Number = parsed.Number
			vars.Repo = parsed.Repo
			vars.OwnerRepo = parsed.OwnerRepo
			mgr := workspace.NewManager(cfg.WorktreesRoot)
			searchDir = mgr.RepoDir(parsed.OwnerRepo)
		}

		// --resource rebinds the resource-derived vars to a resource other than the
		// session's own — a review effect on a PR while its session tracks the issue.
		// The session still supplies the overlay/inputs; only .URL/.Number/.Repo/
		// .OwnerRepo follow the resource.
		if vars, err = applyResourceOverride(vars, templateRenderResource); err != nil {
			return err
		}

		if templateRenderRepo != "" {
			vars.OwnerRepo = templateRenderRepo
			if !resolved {
				mgr := workspace.NewManager(cfg.WorktreesRoot)
				searchDir = mgr.RepoDir(templateRenderRepo)
			}
		}

		vars.Mode = templateName
		vars.Instruction = templateRenderInstruction
		if vars.SessionInputs == nil {
			vars.SessionInputs = map[string]any{}
		}
		maps.Copy(vars.SessionInputs, extra)

		result, err := template.Render(templateName, searchDir, vars)
		if err != nil {
			return err
		}

		fmt.Fprint(os.Stdout, result)
		return nil
	},
}

// applyResourceOverride rebinds the resource-derived template vars to resource
// when it is set, leaving the session-supplied overlay/inputs untouched. An
// empty resource is a no-op so the session's own resource keeps rendering.
func applyResourceOverride(vars template.Vars, resource string) (template.Vars, error) {
	if resource == "" {
		return vars, nil
	}
	parsed, err := github.ParseURL(resource)
	if err != nil {
		return vars, err
	}
	vars.URL = resource
	vars.Number = parsed.Number
	vars.Repo = parsed.Repo
	vars.OwnerRepo = parsed.OwnerRepo
	return vars, nil
}

// parseTemplateVars parses repeated --var key=value flags into a map.
func parseTemplateVars(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --var %q: expected key=value", kv)
		}
		out[k] = v
	}
	return out, nil
}

// templateVarsFromSession builds template vars from a resolved session,
// backfilling the GitHub-shaped fields for sessions that carry them.
func templateVarsFromSession(name string, s *domain.Session) template.Vars {
	var outputs map[string]any
	if s.Tasks != nil {
		if wf := s.Tasks[contractstate.WorkflowPseudoNodeID]; wf != nil {
			outputs = wf.Outputs
		}
	}
	inputs := maps.Clone(s.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	repo := s.OwnerRepo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return template.Vars{
		SessionName:   name,
		ResourceID:    s.ResourceID,
		WorktreePath:  s.WorktreePath,
		Workflow:      outputs,
		SessionInputs: inputs,
		URL:           s.URL,
		Number:        s.Number,
		Repo:          repo,
		OwnerRepo:     s.OwnerRepo,
	}
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available templates with metadata",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		repoDir := ""
		if templateListRepo != "" {
			mgr := workspace.NewManager(cfg.WorktreesRoot)
			repoDir = mgr.RepoDir(templateListRepo)
		}
		templates, err := template.List(repoDir)
		if err != nil {
			return err
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(templates)
	},
}

func init() {
	templateRenderCmd.Flags().StringVar(&templateRenderSession, "session", "", "Session identifier (name, alias, or resource id) to source vars from")
	templateRenderCmd.Flags().StringVar(&templateRenderURL, "url", "", "GitHub issue or PR URL (optional; legacy compat — prefer --session)")
	templateRenderCmd.Flags().StringVar(&templateRenderResource, "resource", "", "Render against this resource id instead of the session's own (rebinds .URL/.Number/.Repo/.OwnerRepo)")
	templateRenderCmd.Flags().StringVar(&templateRenderRepo, "repo", "", "Override owner/repo (default: extracted from URL/session)")
	templateRenderCmd.Flags().StringVar(&templateRenderInstruction, "instruction", "", "Additional instruction text")
	templateRenderCmd.Flags().StringArrayVar(&templateRenderVars, "var", nil, "Explicit template var as key=value (repeatable; exposed as .SessionInputs.<key>)")

	templateListCmd.Flags().StringVar(&templateListRepo, "repo", "", "Filter templates for a specific owner/repo (e.g. owner/repo)")

	templateCmd.AddCommand(templateRenderCmd)
	templateCmd.AddCommand(templateListCmd)
	rootCmd.AddCommand(templateCmd)
}
