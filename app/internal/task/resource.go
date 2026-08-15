package task

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// MatchResourceDef finds the resource definition whose `match` recognizes
// resourceID. Ok=false with a nil error means no definition claims this id —
// a legitimate, common case: most instance-local resources have no declared
// kind, and stay the opaque passthrough dynamic instantiation already
// provides. More than
// one match is a config error surfaced here rather than resolved by picking
// one, since a silent pick would let two definitions silently race for the
// same id space.
func MatchResourceDef(defs map[string]config.ResourceDef, resourceID string) (config.ResourceDef, bool, error) {
	var matched []config.ResourceDef
	for _, def := range defs {
		re, err := regexp.Compile(def.Match)
		if err != nil {
			return config.ResourceDef{}, false, fmt.Errorf("resource %s: `match` %q: %w", def.ID, def.Match, err)
		}
		if re.MatchString(resourceID) {
			matched = append(matched, def)
		}
	}
	switch len(matched) {
	case 0:
		return config.ResourceDef{}, false, nil
	case 1:
		return matched[0], true, nil
	default:
		ids := make([]string, len(matched))
		for i, d := range matched {
			ids[i] = d.ID
		}
		return config.ResourceDef{}, false, fmt.Errorf("resource id %q matches more than one resource definition: %v", resourceID, ids)
	}
}

// ResourceStatus observes a resource id: it finds the resource definition
// whose `match` accepts the id, runs its `observe` script, and validates the
// result against the definition's state schema. Ok=false with a nil error
// means no resource definition recognizes this id — the Resource contract is
// optional, most instance-local resources have no declared kind. branch and
// workdirPath describe the owning session (both empty for a standalone
// `plect resource status` call, which has no owning session) — an observe
// script may derive the current branch from workdirPath as its primary
// identity signal for the resource.
func ResourceStatus(defs map[string]config.ResourceDef, resourceID string, branch string, workdirPath string, mountedPlugins []plugins.Mounted) (map[string]any, config.ResourceDef, bool, error) {
	def, ok, err := MatchResourceDef(defs, resourceID)
	if err != nil || !ok {
		return nil, def, ok, err
	}
	cmdStr, rerr := render(def.Observe, RenderContext{Session: SessionVars{ResourceID: resourceID, Branch: branch, WorkdirPath: workdirPath, Plugins: mountedPlugins}, SourcePath: def.SourcePath})
	if rerr != nil {
		return nil, def, true, fmt.Errorf("resource %s: observe script template: %w", def.ID, rerr)
	}
	stdout, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, def, true, fmt.Errorf("resource %s: %w: %s", def.ID, runErr, msg)
		}
		return nil, def, true, fmt.Errorf("resource %s: %w", def.ID, runErr)
	}
	obj, perr := ParseOutputs(stdout)
	if perr != nil {
		return nil, def, true, fmt.Errorf("resource %s: %w", def.ID, perr)
	}
	schema, serr := CompileSchema(def.StateSchema, def.ResolvedStateSchemaPath(), "resource:"+def.ID)
	if serr != nil {
		return nil, def, true, fmt.Errorf("resource %s: state_schema: %w", def.ID, serr)
	}
	if schema != nil {
		if verr := schema.Validate(obj); verr != nil {
			return nil, def, true, fmt.Errorf("resource %s: observed state does not match state_schema: %s", def.ID, DescribeValidationError(schema, verr))
		}
	}
	return normalizeOutputs(obj), def, true, nil
}

// FinalizeJudgeEvidence is one judge leaf's satisfied verdict, carried into a
// resource's finalize script as evidence of who approved what (ADR
// "goal-as-task" D4: the completion record cites judge id, reason, and the
// revision it was recorded against).
type FinalizeJudgeEvidence struct {
	ID               string `json:"id"`
	Reason           string `json:"reason,omitempty"`
	Revision         string `json:"revision,omitempty"`
	ReviewerSession  string `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string `json:"reviewer_workflow,omitempty"`
	Relation         string `json:"relation,omitempty"`
}

// FinalizeResourceParams is the evidence and identity supplied to a resource
// definition's `finalize` script.
type FinalizeResourceParams struct {
	ResourceID  string
	Instance    string
	SessionName string
	Revision    string
	Judges      []FinalizeJudgeEvidence
	// Plugins are the mounted plugins `{{bin ...}}` resolves against inside
	// finalize (config.Config.Plugins) — mirrors Observe's mountedPlugins
	// parameter above.
	Plugins []plugins.Mounted
}

// finalizeTemplateData is the template surface a `finalize` script renders
// against — a small, dedicated struct rather than RenderContext/SessionVars:
// the judge evidence it carries belongs to the task instance being
// finalized, not to the resource's own session, so reusing the task
// setup/cleanup template surface would conflate the two.
type finalizeTemplateData struct {
	ResourceID  string
	Instance    string
	SessionName string
	Revision    string
	Judges      []FinalizeJudgeEvidence
	JudgesJSON  string
	Plugins     []plugins.Mounted
	SourcePath  string
}

// FinalizeResource runs the matched resource definition's `finalize` script,
// if it declares one. Ran=false with a nil error covers both "no definition
// recognizes this resource id" and "the definition declares no finalize" —
// every resource kind until a later ADR slice adds one (e.g. local-okf). Core
// commits to nothing beyond "there was no completion record to write";
// `plect task finalize` treats that as expected, not an error.
func FinalizeResource(defs map[string]config.ResourceDef, params FinalizeResourceParams) (ran bool, def config.ResourceDef, err error) {
	def, ok, merr := MatchResourceDef(defs, params.ResourceID)
	if merr != nil {
		return false, def, merr
	}
	if !ok || strings.TrimSpace(def.Finalize) == "" {
		return false, def, nil
	}
	judgesJSON, jerr := marshalJudges(params.Judges)
	if jerr != nil {
		return false, def, fmt.Errorf("resource %s: finalize: %w", def.ID, jerr)
	}
	cmdStr, rerr := renderFinalize(def.Finalize, finalizeTemplateData{
		ResourceID:  params.ResourceID,
		Instance:    params.Instance,
		SessionName: params.SessionName,
		Revision:    params.Revision,
		Judges:      params.Judges,
		JudgesJSON:  judgesJSON,
		Plugins:     params.Plugins,
		SourcePath:  def.SourcePath,
	})
	if rerr != nil {
		return false, def, fmt.Errorf("resource %s: finalize script template: %w", def.ID, rerr)
	}
	_, stderr, runErr := runShell(cmdStr, "")
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return false, def, fmt.Errorf("resource %s: finalize: %w: %s", def.ID, runErr, msg)
		}
		return false, def, fmt.Errorf("resource %s: finalize: %w", def.ID, runErr)
	}
	return true, def, nil
}

func marshalJudges(judges []FinalizeJudgeEvidence) (string, error) {
	if len(judges) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(judges)
	if err != nil {
		return "", fmt.Errorf("marshal judge evidence: %w", err)
	}
	return string(b), nil
}

func renderFinalize(tmplStr string, data finalizeTemplateData) (string, error) {
	// bin is built per render call, not part of the static templateFuncs map,
	// because it resolves against this render's own data.Plugins — mirrors
	// renderWith's dynamicFuncs for the same reason.
	dynamicFuncs := template.FuncMap{
		"bin": func(ref string) (string, error) {
			return plugins.ResolveBin(data.Plugins, data.SourcePath, ref)
		},
	}
	tmpl, err := template.New("resource-finalize").
		Option("missingkey=error").
		Funcs(templateFuncs).
		Funcs(dynamicFuncs).
		Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return buf.String(), nil
}
