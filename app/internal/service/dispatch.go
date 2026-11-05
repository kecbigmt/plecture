package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
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
// against the resolved session name, before any setup side task.
// The orchestrator's pane exports a guard like "^acme/" so a cross-owner
// dispatch (which resolves to "exampleorg/...") is rejected server-side rather
// than relying on the loop-spec prompt. plect core never parses the owner — the
// guard is an opaque regex its caller supplied.
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

type dispatchResult struct {
	Workflow    config.WorkflowFile
	ResourceKey string
	Name        string
}

// resolveResource treats two matching declarations as an ambiguity, since the
// resource type decides which workflow accepts the identifier.
func resolveResource(resources map[string]config.ResourceDef, id string) (key, name string, matched bool, err error) {
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var hits []string
	for _, k := range keys {
		n, ok, nameErr := resources[k].InstanceName(id)
		if nameErr != nil {
			return "", "", false, nameErr
		}
		if ok {
			hits = append(hits, k)
			key, name = k, n
		}
	}
	switch len(hits) {
	case 0:
		return "", "", false, nil
	case 1:
		return key, name, true, nil
	}
	return "", "", false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches more than one resource declaration (%s)", id, strings.Join(hits, ", "))}
}

func workflowAutoSelect(wf config.WorkflowFile) bool {
	return wf.AutoSelect == nil || *wf.AutoSelect
}

// dispatchResource selects the workflow for a resource identifier by its entry
// resource type. matched is false, with no error, for an identifier no resource
// recognizes, so a caller may treat it as a session name.
func dispatchResource(cfg *config.Config, flag, resource string) (result dispatchResult, matched bool, err error) {
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
	}
	resources, err := cfg.LoadResourceDefs()
	if err != nil {
		return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load resources: %v", err)}
	}
	key, name, recognized, err := resolveResource(resources, resource)
	if err != nil {
		if svcErr, ok := err.(*Error); ok {
			return dispatchResult{}, false, svcErr
		}
		return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if !recognized {
		return dispatchResult{}, false, nil
	}

	if flag != "" {
		wf, ok := workflows[flag]
		if !ok {
			return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found; add workflows/%s.toml to the global config or a plugin", flag, flag)}
		}
		if wf.Resource != key {
			return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q accepts resource %q, but %q resolves to resource %q", flag, wf.Resource, resource, key)}
		}
		return dispatchResult{Workflow: wf, ResourceKey: key, Name: name}, true, nil
	}

	var candidates []config.WorkflowFile
	addresses := config.Addresses(workflows)
	sort.Strings(addresses)
	for _, address := range addresses {
		wf := workflows[address]
		if wf.Resource == key && workflowAutoSelect(wf) {
			candidates = append(candidates, wf)
		}
	}
	switch len(candidates) {
	case 0:
		return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no workflow accepts resource %q (the type of %q); declare one with `resource = %q` or pass --workflow", key, resource, key)}
	case 1:
		return dispatchResult{Workflow: candidates[0], ResourceKey: key, Name: name}, true, nil
	}
	// Addresses, not ids: two plugins may declare one id, and a list that
	// printed it twice would name nothing the reader could pass back.
	named := make([]string, len(candidates))
	for i, wf := range candidates {
		named[i] = wf.Address
	}
	return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource %q matches multiple workflows (%s); pass --workflow to choose", key, strings.Join(named, ", "))}
}
