package service

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

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
// against the resolved session name, before any workdir/setup side task.
// The orchestrator's pane exports a guard like "^acme/" so a cross-owner
// dispatch (which resolves to "exampleorg/...") is rejected server-side rather
// than relying on the loop-spec prompt. plect core never parses the owner — the
// guard is an opaque regex the provider supplied.
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
// the resource, the provider that resolved it, and the derived session id
// (before any +tag suffix).
type dispatchResult struct {
	Workflow config.WorkflowFile
	Provider config.ProviderConfig
	Name     string
}

// resolveName runs a provider's resolver against the resource id. A
// non-matching input is an error — the regex doubles as input validation for
// explicitly-selected workflows.
func resolveName(prov config.ProviderConfig, resource string) (string, error) {
	name, ok, err := tryResolveName(prov, resource)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("resource %q does not match provider %q resolver (%s)", resource, prov.ID, prov.Match)
	}
	return name, nil
}

// tryResolveName attempts the resolver match, reporting (name, matched, err).
// Pure: regex + template only, no I/O.
func tryResolveName(prov config.ProviderConfig, resource string) (string, bool, error) {
	re, err := regexp.Compile(prov.Match)
	if err != nil {
		return "", false, fmt.Errorf("provider %q resolver match: %w", prov.ID, err)
	}
	m := re.FindStringSubmatch(resource)
	if m == nil {
		return "", false, nil
	}
	captures := map[string]string{}
	for i, group := range re.SubexpNames() {
		if group != "" && i < len(m) {
			captures[group] = m[i]
		}
	}
	tmpl, err := template.New("resolver").Option("missingkey=error").Parse(prov.Name)
	if err != nil {
		return "", false, fmt.Errorf("provider %q resolver name template: %w", prov.ID, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, captures); err != nil {
		return "", false, fmt.Errorf("provider %q resolver name template: %w", prov.ID, err)
	}
	name := strings.TrimSpace(buf.String())
	if name == "" {
		return "", false, fmt.Errorf("provider %q resolver produced an empty session id for %q", prov.ID, resource)
	}
	return name, true, nil
}

// providerFor resolves a workflow's provider reference. ok=false when the
// workflow declares no provider, which leaves it unable to back a session.
// A dangling reference is an error — silently ignoring it would route the
// create to the wrong path.
func providerFor(wf config.WorkflowFile, providers map[string]config.ProviderConfig) (config.ProviderConfig, bool, error) {
	if wf.Provider == "" {
		return config.ProviderConfig{}, false, nil
	}
	prov, ok := providers[wf.Provider]
	if !ok {
		return config.ProviderConfig{}, false, fmt.Errorf("workflow %q references unknown provider %q; add providers/%s.toml to the global config or a plugin", wf.ID, wf.Provider, wf.Provider)
	}
	return prov, true, nil
}

func workflowAutoSelect(wf config.WorkflowFile) bool {
	return wf.AutoSelect == nil || *wf.AutoSelect
}

// dispatchResource selects the workflow for a resource identifier.
//
//	flag != "" — that workflow handles it; its provider's resolver derives
//	             the name, and a resolver mismatch is an error.
//	flag == "" — every trusted-layer workflow whose provider has a resolver
//	             tries to match. Exactly one match wins; several is an
//	             ambiguity error; zero returns ok=false so the caller can
//	             fall back to identity dispatch. Workflows without a
//	             resolver never participate — identity dispatch on arbitrary
//	             input would shadow every other workflow.
//
// Dispatch reads only the trusted base layers (plugin + global): the working
// directory doesn't exist yet, and identity must not depend on clone content.
func dispatchResource(cfg *config.Config, flag, resource string) (dispatchResult, bool, error) {
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return dispatchResult{}, false, fmt.Errorf("load workflows: %w", err)
	}
	providers, err := cfg.LoadProviders()
	if err != nil {
		return dispatchResult{}, false, fmt.Errorf("load providers: %w", err)
	}

	if flag != "" {
		wf, ok := workflows[flag]
		if !ok {
			// Not in the trusted base layers. Fall through so the identity
			// branch surfaces its own not-found message, which can name the
			// file the user needs to add.
			return dispatchResult{}, false, nil
		}
		prov, ok, provErr := providerFor(wf, providers)
		if provErr != nil {
			return dispatchResult{}, false, &Error{Code: ErrExecutionFailed, Message: provErr.Error()}
		}
		if !ok || !prov.HasResolver() {
			// Identity is the caller's branch — it needs context (is the
			// workflow provider-backed at all?) that dispatch doesn't have.
			return dispatchResult{}, false, nil
		}
		name, err := resolveName(prov, resource)
		if err != nil {
			return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: err.Error()}
		}
		return dispatchResult{Workflow: wf, Provider: prov, Name: name}, true, nil
	}

	type candidate struct {
		wf   config.WorkflowFile
		prov config.ProviderConfig
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
		prov, ok, provErr := providerFor(wf, providers)
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
		return dispatchResult{Workflow: matches[0].wf, Provider: matches[0].prov, Name: matches[0].name}, true, nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.wf.ID
		}
		return dispatchResult{}, false, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource matches multiple workflow resolvers (%s); pass --workflow to choose", strings.Join(names, ", "))}
	}
}
