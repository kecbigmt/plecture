package commands

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/template"
)

var (
	templateRenderSession      string
	templateRenderWorkspaceDir string
	templateRenderInstruction  string
	templateRenderVars         []string
	templateListWorkspaceDir   string
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage templates",
}

var templateRenderCmd = &cobra.Command{
	Use:   "render <template-name>",
	Short: "Render a template to stdout",
	Long: `Render a template with the given variables and print to stdout.

Variables come from a session. Pass --session <identifier> (session name,
alias, or resource id) to resolve a session and expose its vars to the
template:
  {{.SessionName}} {{.ResourceID}} {{.WorkspaceDirPath}}
  {{.Workflow.outputs.<key>}}  — workspace provider setup outputs (workspace_dir, plus whatever else the workspace provider declares)
  {{.SessionInputs.<key>}}     — session inputs and explicit --var values

Guard optional vars with {{get .SessionInputs "key" "default"}} (yields the
third argument when the key is absent) instead of {{.SessionInputs.key}}
(renders "<no value>"). The default is required: pass "" for an absent key
that should render as nothing.

The template name corresponds to files in the template search path:
  1. <workspace-dir>/.plect/templates/<name>.md  (session workspace-directory overlay)
  2. ~/.config/plect/templates/<name>.md`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templateName := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		extra, err := parseTemplateVars(templateRenderVars)
		if err != nil {
			return err
		}

		var vars template.Vars
		searchDir := ""
		resolved := false

		// A session identifier yields the session's vars and roots the
		// template overlay at its workspace directory.
		if templateRenderSession != "" {
			store := state.NewStore("")
			tv, rerr := service.ResolveTemplateVars(cfg, store, templateRenderSession)
			if rerr != nil {
				return rerr
			}
			vars = template.Vars{
				SessionName:      tv.SessionName,
				ResourceID:       tv.ResourceID,
				WorkspaceDirPath: tv.WorkspaceDirPath,
				Workflow:         tv.Workflow,
				SessionInputs:    tv.SessionInputs,
			}
			searchDir = tv.WorkspaceDirPath
			resolved = true
		}

		if templateRenderWorkspaceDir != "" && !resolved {
			searchDir = templateRenderWorkspaceDir
		}

		vars.Mode = templateName
		vars.Instruction = templateRenderInstruction
		if vars.SessionInputs == nil {
			vars.SessionInputs = map[string]any{}
		}
		maps.Copy(vars.SessionInputs, extra)

		result, err := template.Render(templateName, searchDir, cfg.PluginDirs, vars)
		if err != nil {
			return err
		}

		fmt.Fprint(os.Stdout, result)
		return nil
	},
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

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available templates with metadata",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		workspaceDirPath := ""
		if templateListWorkspaceDir != "" {
			workspaceDirPath = templateListWorkspaceDir
		}
		templates, err := template.List(workspaceDirPath, cfg.PluginDirs)
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
	templateRenderCmd.Flags().StringVar(&templateRenderWorkspaceDir, "workspace-dir", "", "Workspace directory path to root the template overlay at")
	templateRenderCmd.Flags().StringVar(&templateRenderInstruction, "instruction", "", "Additional instruction text")
	templateRenderCmd.Flags().StringArrayVar(&templateRenderVars, "var", nil, "Explicit template var as key=value (repeatable; exposed as .SessionInputs.<key>)")

	templateListCmd.Flags().StringVar(&templateListWorkspaceDir, "workspace-dir", "", "Workspace directory path to include templates from")

	templateCmd.AddCommand(templateRenderCmd)
	templateCmd.AddCommand(templateListCmd)
	rootCmd.AddCommand(templateCmd)
}
