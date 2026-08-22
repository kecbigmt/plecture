package configlang

import (
	"fmt"
	"regexp"
)

// ObserveFunc runs one resource observer's observe action against a resource
// and returns the state it published. A non-zero exit is an error here: the
// observer's exit code signals health, and what that health costs depends on
// which observation it was.
type ObserveFunc func(observer *Definition, resourceID string) (map[string]any, error)

// Instance is a task document bound to one resource. The document is
// type-declared and instance-late-bound: which resource it learns here, what
// kind of resource it stated up front.
type Instance struct {
	Task       *Definition
	Observer   *Definition
	ResourceID string

	state    map[string]any
	degraded error
}

// Instantiate binds a task document to a resource. It checks the resource
// against the observer the document declares, then observes it once —
// failing closed on that first observation, so a task instance never exists
// in a state its own resource cannot support.
func (v Validation) Instantiate(def *Definition, r *Registry, resourceID string, observe ObserveFunc) (*Instance, error) {
	pos := Position{File: def.File, Path: def.ID}
	at := childPos(pos, "resource_observer")
	raw, ok := def.Body["resource_observer"]
	if !ok {
		return nil, newDiag(CodeFieldRequired, LayerStructural, at,
			"a task document declares the resource observer it is written for")
	}
	ref, err := staticRef(raw, at.Path)
	if err != nil {
		return nil, err
	}
	observer, err := r.ExpectKind(ref, v.From, KindResourceObserver, at.Path)
	if err != nil {
		return nil, err
	}
	if !recognizes(observer, resourceID) {
		return nil, newDiag(CodeResourceObserverMismatch, LayerInstantiation, pos,
			fmt.Sprintf("%q does not resolve to %q, the observer this document is written for", resourceID, ref))
	}
	state, err := observe(observer, resourceID)
	if err != nil {
		return nil, newDiag(CodeFirstObserveFailed, LayerInstantiation, pos,
			fmt.Sprintf("observing %s: %v", resourceID, err))
	}
	return &Instance{Task: def, Observer: observer, ResourceID: resourceID, state: state}, nil
}

// Observe records one later observation. It returns nothing because a later
// failure is degradation rather than an error to act on: the instance
// survives it, and State is where the caller learns the difference.
func (i *Instance) Observe(observe ObserveFunc) {
	state, err := observe(i.Observer, i.ResourceID)
	if err != nil {
		i.state, i.degraded = nil, err
		return
	}
	i.state, i.degraded = state, nil
}

// State is the resource state this instance's completion predicate reads.
// While the last observation is a failed one it reads as unobserved, so a
// predicate is never satisfied by a snapshot the resource has stopped
// supporting.
func (i *Instance) State() (map[string]any, error) {
	if i.degraded != nil {
		return nil, i.degraded
	}
	return i.state, nil
}

// recognizes reports whether the observer's match claims this resource id.
// An observer declaring no match recognizes nothing, which is the same
// answer as one whose match rejects the id: neither can say what this
// resource is.
func recognizes(observer *Definition, resourceID string) bool {
	pattern, ok := observer.Body["match"].(string)
	if !ok {
		return false
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(resourceID)
}
