package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

// validSessionNameRE is plect's own conservative session id charset: ids
// become state keys, runtime channel identifiers (whatever the declared
// runtime task uses them for — a terminal multiplexer session name today),
// and log labels, so it stays deliberately narrow rather than tracking any
// one runtime's accepted charset. The first character must be alphanumeric
// so an id never looks like a CLI flag.
var validSessionNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/_+-]*$`)

// validateSessionName enforces the session id grammar on both resolver-derived
// and identity (user-typed) ids.
func validateSessionName(name string) *Error {
	if !validSessionNameRE.MatchString(name) {
		return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("invalid session id %q: must match %s", name, validSessionNameRE.String())}
	}
	return nil
}

// checkSessionGuard enforces the per-session SessionGuard (PLECT_SESSION_GUARD)
// against the resolved session name, before any workspace/setup side task.
// The orchestrator's pane exports a guard like "^acme/" so a cross-owner
// dispatch (which resolves to "exampleorg/...") is rejected server-side rather
// than relying on the loop-spec prompt. plect core never parses the owner — the
// guard is an opaque regex the workspace provider supplied.
func checkSessionGuard(cfg *config.Config, sessionName string) *Error {
	// No config means no guard configured — allow (mirrors an empty guard).
	// EventPublish and other write paths accept a nil cfg from in-process
	// callers and tests; the guard is only ever populated from the environment.
	if cfg == nil {
		return nil
	}
	allowed, err := cfg.IsSessionNameAllowed(sessionName)
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if !allowed {
		return &Error{Code: ErrRepoNotAllowed, Message: fmt.Sprintf("session %q is outside the active session guard %q", sessionName, cfg.SessionGuard)}
	}
	return nil
}

// SessionGuardForOwnSession is for a per-session `plect mcp listen` socket:
// injecting the result into every spawned `plect mcp serve` child's env makes
// the socket's scope independent of whatever PLECT_SESSION_GUARD the listening
// process itself inherited. sessionName is quoted so its regex
// metacharacters (e.g. the "+" tag separator) match literally.
//
// Validation happens here, before any path.Join, because a caller may derive
// a filesystem socket path from sessionName — the id grammar's charset has
// no ".", which is what stops a "../" component from escaping that path.
func SessionGuardForOwnSession(sessionName string) (string, error) {
	if err := validateSessionName(sessionName); err != nil {
		return "", err
	}
	return "^" + regexp.QuoteMeta(sessionName) + "($|/)", nil
}

// dispatchResult is the outcome of resolver dispatch: which workflow handles
// the resource, the workspace provider that resolved it, and the derived
// session id (before any +tag suffix).
type dispatchResult struct {
	Workflow          config.WorkflowFile
	WorkspaceProvider config.WorkspaceProviderConfig
	Name              string
}

// resolveName runs a workspace provider's resolver against the resource id. A
// non-matching input is an error — the regex doubles as input validation for
// explicitly-selected workflows.
func resolveName(prov config.WorkspaceProviderConfig, resource string) (string, error) {
	name, ok, err := tryResolveName(prov, resource)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("resource %q does not match workspace provider %q resolver (%s)", resource, prov.ID, prov.Match)
	}
	return name, nil
}

// tryResolveName attempts the resolver match, reporting (name, matched, err).
// Pure: a regex match and one value resolution, no I/O.
func tryResolveName(prov config.WorkspaceProviderConfig, resource string) (string, bool, error) {
	re, err := regexp.Compile(prov.Match)
	if err != nil {
		return "", false, fmt.Errorf("workspace provider %q resolver match: %w", prov.ID, err)
	}
	m := re.FindStringSubmatch(resource)
	if m == nil {
		return "", false, nil
	}
	captures := map[string]any{}
	for i, group := range re.SubexpNames() {
		if group != "" && i < len(m) {
			captures[group] = m[i]
		}
	}
	eval := lang.Eval{Env: lang.Environment{"match": captures}}
	resolved, _, err := eval.Argument(prov.Name)
	if err != nil {
		return "", false, fmt.Errorf("workspace provider %q resolver name: %w", prov.ID, err)
	}
	name := strings.TrimSpace(resolved)
	if name == "" {
		return "", false, fmt.Errorf("workspace provider %q resolver produced an empty session id for %q", prov.ID, resource)
	}
	return name, true, nil
}

// workspaceProviderFor resolves a workflow's workspace provider reference.
// ok=false when the workflow declares no workspace provider, which leaves it
// unable to back a session. A dangling reference is an error — silently
// ignoring it would route the create to the wrong path.
func workspaceProviderFor(wf config.WorkflowFile, workspaceProviders map[string]config.WorkspaceProviderConfig) (config.WorkspaceProviderConfig, bool, error) {
	if wf.WorkspaceProvider == "" {
		return config.WorkspaceProviderConfig{}, false, nil
	}
	prov, ok := workspaceProviders[wf.WorkspaceProvider]
	if !ok {
		return config.WorkspaceProviderConfig{}, false, fmt.Errorf("workflow %q references unknown workspace provider %q; add workspaces/%s.toml to the global config or a plugin", wf.ID, wf.WorkspaceProvider, wf.WorkspaceProvider)
	}
	return prov, true, nil
}

func workflowAutoSelect(wf config.WorkflowFile) bool {
	return wf.AutoSelect == nil || *wf.AutoSelect
}

// dispatchResource selects the workflow for a resource identifier.
//
//	flag != "" — that workflow handles it; its workspace provider's resolver
//	             derives the name, and a resolver mismatch is an error.
//	flag == "" — every trusted-layer workflow whose workspace provider has a
//	             resolver tries to match. Exactly one match wins; several is
//	             an ambiguity error; zero returns ok=false so the caller can
//	             fall back to identity dispatch. Workflows without a
//	             resolver never participate — identity dispatch on arbitrary
//	             input would shadow every other workflow.
//
// Dispatch reads only the trusted base layers (plugin + global): the
// workspace doesn't exist yet, and identity must not depend on clone content.
func dispatchResource(cfg *config.Config, flag, resource string) (dispatchResult, bool, error) {
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return dispatchResult{}, false, fmt.Errorf("load workflows: %w", err)
	}
	workspaceProviders, err := cfg.LoadWorkspaceProviders()
	if err != nil {
		return dispatchResult{}, false, fmt.Errorf("load workspace providers: %w", err)
	}

	if flag != "" {
		wf, ok := workflows[flag]
		if !ok {
			// Not in the trusted base layers. Fall through so the identity
			// branch surfaces its own not-found message, which can name the
			// file the user needs to add.
			return dispatchResult{}, false, nil
		}
		prov, ok, provErr := workspaceProviderFor(wf, workspaceProviders)
		if provErr != nil {
			return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: provErr.Error()}
		}
		if !ok || !prov.HasResolver() {
			// Identity is the caller's branch — it needs context (is the
			// workflow workspace-provider-backed at all?) that dispatch
			// doesn't have.
			return dispatchResult{}, false, nil
		}
		name, err := resolveName(prov, resource)
		if err != nil {
			return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: err.Error()}
		}
		return dispatchResult{Workflow: wf, WorkspaceProvider: prov, Name: name}, true, nil
	}

	type candidate struct {
		wf   config.WorkflowFile
		prov config.WorkspaceProviderConfig
		name string
	}
	var matches []candidate
	ids := make([]string, 0, len(workflows))
	for id := range workflows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		wf := workflows[id]
		if !workflowAutoSelect(wf) {
			continue
		}
		prov, ok, provErr := workspaceProviderFor(wf, workspaceProviders)
		if provErr != nil {
			return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: provErr.Error()}
		}
		if !ok || !prov.HasResolver() {
			continue
		}
		name, matched, err := tryResolveName(prov, resource)
		if err != nil {
			return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		if matched {
			matches = append(matches, candidate{wf: wf, prov: prov, name: name})
		}
	}
	switch len(matches) {
	case 0:
		return dispatchResult{}, false, nil
	case 1:
		return dispatchResult{Workflow: matches[0].wf, WorkspaceProvider: matches[0].prov, Name: matches[0].name}, true, nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.wf.ID
		}
		return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource matches multiple workflow resolvers (%s); pass --workflow to choose", strings.Join(names, ", "))}
	}
}
