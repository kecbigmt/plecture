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
	templateRenderSession     string
	templateRenderWorkdir     string
	templateRenderInstruction string
	templateRenderVars        []string
	templateListWorkdir       string
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
  {{.SessionName}} {{.ResourceID}} {{.WorkdirPath}}
  {{.Workflow.outputs.<key>}}  — provider setup outputs (workdir, branch, ...)
  {{.SessionInputs.<key>}}     — session inputs and explicit --var values

Guard optional vars with {{get .SessionInputs "key"}} (returns "" when absent)
instead of {{.SessionInputs.key}} (renders "<no value>").

The template name corresponds to files in the template search path:
  1. <workdir>/.plect/templates/<name>.md  (session working-directory overlay)
  2. ~/.config/plect/templates/<name>.md`,
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

		// A session identifier yields the session's vars and roots the
		// template overlay at its working directory.
		if templateRenderSession != "" {
			store := state.NewStore("")
			tv, rerr := service.ResolveTemplateVars(cfg, store, templateRenderSession)
			if rerr != nil {
				return rerr
			}
			vars = template.Vars{
				SessionName:   tv.SessionName,
				ResourceID:    tv.ResourceID,
				WorkdirPath:   tv.WorkdirPath,
				Workflow:      tv.Workflow,
				SessionInputs: tv.SessionInputs,
			}
			searchDir = tv.WorkdirPath
			resolved = true
		}

		if templateRenderWorkdir != "" && !resolved {
			searchDir = templateRenderWorkdir
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
		workdir := ""
		if templateListWorkdir != "" {
			workdir = templateListWorkdir
		}
		templates, err := template.List(workdir)
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
	templateRenderCmd.Flags().StringVar(&templateRenderWorkdir, "workdir", "", "Working directory path to root the template overlay at")
	templateRenderCmd.Flags().StringVar(&templateRenderInstruction, "instruction", "", "Additional instruction text")
	templateRenderCmd.Flags().StringArrayVar(&templateRenderVars, "var", nil, "Explicit template var as key=value (repeatable; exposed as .SessionInputs.<key>)")

	templateListCmd.Flags().StringVar(&templateListWorkdir, "workdir", "", "Working directory path to include templates from")

	templateCmd.AddCommand(templateRenderCmd)
	templateCmd.AddCommand(templateListCmd)
	rootCmd.AddCommand(templateCmd)
}
