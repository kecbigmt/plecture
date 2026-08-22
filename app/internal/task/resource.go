package task

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// MatchResourceDef finds the resource observer whose `match` recognizes
// resourceID. Ok=false with a nil error means no observer claims this id —
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

// ResourceStatus observes a resource id: it finds the resource observer whose
// `match` accepts the id, runs its `observe` action, and validates the
// result against the observer's state schema. Ok=false with a nil error
// means no observer recognizes this id — the Resource contract is
// optional, most instance-local resources have no declared kind. branch and
// workspaceDirPath describe the owning session (both empty for a standalone
// `plect resource status` call, which has no owning session) — an observe
// action may derive the current branch from the workspace directory as its
// primary identity signal for the resource.
func ResourceStatus(defs map[string]config.ResourceDef, resourceID string, branch string, workspaceDirPath string, mountedPlugins []plugins.Mounted) (map[string]any, config.ResourceDef, bool, error) {
	def, ok, err := MatchResourceDef(defs, resourceID)
	if err != nil || !ok {
		return nil, def, ok, err
	}
	env := lang.Environment{"resource": map[string]any{"id": resourceID}}
	// An absent workspace is absent from the environment rather than present
	// and empty: that is what lets a standalone observation's `default` fire
	// instead of handing the executable an empty flag value it cannot tell
	// from a real one.
	if workspace := presentKeys(map[string]string{"dir": workspaceDirPath, "branch": branch}); workspace != nil {
		env["workspace"] = workspace
	}
	stdout, stderr, runErr := runResourceAction(def, def.Observe, env, mountedPlugins)
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
// resource's finalize action as evidence of who approved what (ADR
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
// observer's `finalize` action.
type FinalizeResourceParams struct {
	ResourceID  string
	SessionName string
	Revision    string
	Judges      []FinalizeJudgeEvidence
	// Plugins are the mounted plugins a `bin` reference resolves against
	// inside finalize (config.Config.Plugins) — mirrors Observe's
	// mountedPlugins parameter above.
	Plugins []plugins.Mounted
}

// FinalizeResource runs the matched observer's `finalize` action, if it
// declares one. Ran=false with a nil error covers both "no observer
// recognizes this resource id" and "the observer declares no finalize" —
// every resource kind until a later ADR slice adds one (e.g. local-okf). Core
// commits to nothing beyond "there was no completion record to write";
// `plect task finalize` treats that as expected, not an error.
func FinalizeResource(defs map[string]config.ResourceDef, params FinalizeResourceParams) (ran bool, def config.ResourceDef, err error) {
	def, ok, merr := MatchResourceDef(defs, params.ResourceID)
	if merr != nil {
		return false, def, merr
	}
	if !ok || def.Finalize == nil {
		return false, def, nil
	}
	judges := make([]any, 0, len(params.Judges))
	for _, judge := range params.Judges {
		judges = append(judges, judge)
	}
	env := lang.Environment{
		"resource": map[string]any{"id": params.ResourceID, "revision": params.Revision},
		"judges":   judges,
	}
	if session := presentKeys(map[string]string{"name": params.SessionName}); session != nil {
		env["session"] = session
	}
	_, stderr, runErr := runResourceAction(def, def.Finalize, env, params.Plugins)
	if runErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return false, def, fmt.Errorf("resource %s: finalize: %w: %s", def.ID, runErr, msg)
		}
		return false, def, fmt.Errorf("resource %s: finalize: %w", def.ID, runErr)
	}
	return true, def, nil
}

// runResourceAction resolves one of an observer's actions against env and
// runs it. A shell action gets a private run directory for the binding
// transport, created only when there is a shell action to transport
// bindings for — an observation runs on every tick, and an exec action
// touches no filesystem of its own.
func runResourceAction(def config.ResourceDef, action *lang.Action, env lang.Environment, mountedPlugins []plugins.Mounted) (stdout, stderr []byte, err error) {
	bins := config.MountedBins{Mounted: mountedPlugins, SourcePath: def.SourcePath}
	eval := lang.Eval{
		Env: env,
		Bin: func(ref string) (string, error) { return bins.ResolveBin(ref, def.Ownership()) },
	}
	runDir := ""
	if action.Type == lang.ActionShell {
		dir, mkErr := os.MkdirTemp("", "plect-resource-")
		if mkErr != nil {
			return nil, nil, mkErr
		}
		defer os.RemoveAll(dir)
		runDir = dir
	}
	execution, err := eval.Run(runDir, action, nil)
	if err != nil {
		return nil, nil, err
	}
	return alwaysHostExecutor.Run(context.Background(), ExecRequest{Argv: execution.Argv, Stdin: execution.Stdin})
}

// presentKeys drops the empty entries of a root's keys, and the root itself
// when nothing is left, so absence stays distinguishable from emptiness.
func presentKeys(keys map[string]string) map[string]any {
	var out map[string]any
	for key, value := range keys {
		if value == "" {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(keys))
		}
		out[key] = value
	}
	return out
}
