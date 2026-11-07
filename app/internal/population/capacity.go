package population

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kecbigmt/plecture/app/internal/admitstatus"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type capacityCoordinator struct {
	cfg   func() *config.Config
	state *state.Store
	log   *eventlog.Store
	cache *admitstatus.Cache
	mu    sync.Mutex // serializes the evict-then-retry decision in up()

	// definitions has its own lock, separate from mu: pendingExistingAhead
	// (and the cache scan a miss there can trigger) reads it ahead of
	// acquiring mu, so that read can never block every other population's
	// concurrent admission attempt on one member's history scan.
	defMu       sync.RWMutex
	definitions map[contract.PopulationProvenance]Definition
}

func newCapacityCoordinator(cfg func() *config.Config, stateStore *state.Store, logStore *eventlog.Store, cache *admitstatus.Cache) *capacityCoordinator {
	return &capacityCoordinator{cfg: cfg, state: stateStore, log: logStore, cache: cache, definitions: make(map[contract.PopulationProvenance]Definition)}
}

func (c *capacityCoordinator) setDefinitions(definitions []Definition) {
	next := make(map[contract.PopulationProvenance]Definition, len(definitions))
	for _, definition := range definitions {
		next[populationKey(definition)] = definition
	}
	c.defMu.Lock()
	c.definitions = next
	c.defMu.Unlock()
}

func (c *capacityCoordinator) definition(key contract.PopulationProvenance) (Definition, bool) {
	c.defMu.RLock()
	defer c.defMu.RUnlock()
	def, ok := c.definitions[key]
	return def, ok
}

func (c *capacityCoordinator) definitionKeys() []contract.PopulationProvenance {
	c.defMu.RLock()
	defer c.defMu.RUnlock()
	keys := make([]contract.PopulationProvenance, 0, len(c.definitions))
	for key := range c.definitions {
		keys = append(keys, key)
	}
	return keys
}

func (c *capacityCoordinator) up(_ context.Context, def Definition, resource string, inputs map[string]any) (UpOutcome, error) {
	provenance := populationKey(def)
	outcome, err := upPopulation(c.cfg, c.state, def, &provenance, resource, inputs)
	if err != nil || !outcome.Sleeping {
		return outcome, err
	}
	if blocker, blocked := c.pendingExistingAhead(def, resource); blocked {
		return UpOutcome{}, &pendingPriorityError{population: blocker.key, resource: blocker.resource, session: blocker.session}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// A distinct variable, not err: a zero-candidate result isn't an error.
	candidates, candidatesErr := c.idleCandidates()
	if candidatesErr != nil {
		return UpOutcome{}, fmt.Errorf("find idle candidates to free capacity: %w", candidatesErr)
	}
	for _, candidate := range candidates {
		if _, downErr := service.Down(c.cfg(), c.state, service.DownParams{Identifier: candidate.session, Sleep: true}); downErr != nil {
			c.record(candidate, event.TypeWorkflowPopulationFailure, "down", downErr.Error())
			continue
		}
		c.record(candidate, event.TypeWorkflowPopulationDown, "capacity", "population member brought down for virtual-root capacity")
		outcome, err = upPopulation(c.cfg, c.state, def, &provenance, resource, inputs)
		if err != nil || !outcome.Sleeping {
			return outcome, err
		}
	}
	if target, resolveErr := service.ResolvePopulationSessionName(c.cfg(), def.ProjectRoot, def.Workflow.Address, resource); resolveErr == nil {
		c.record(idleCandidate{session: target, resource: resource, key: populationKey(def)}, event.TypeWorkflowPopulationDown,
			"capacity", "virtual-root capacity remains full with no eligible population member to bring down")
	}
	return outcome, nil
}

func isCapError(err error) bool {
	var serviceErr *service.Error
	return errors.As(err, &serviceErr) && serviceErr.Code == service.ErrChildCapExceeded
}

type pendingPriorityError struct {
	population contract.PopulationProvenance
	resource   string
	session    string
}

func (e *pendingPriorityError) Error() string {
	return fmt.Sprintf(
		"existing population member %q (population %s, resource %q) has a pending up request and takes priority",
		e.session, populationLabel(e.population), e.resource,
	)
}

// isCapError alone would miss pendingPriorityError: a member preempted by
// someone else's priority is itself still just capacity-blocked, not broken.
func isCapacityRefusal(err error) bool {
	var priority *pendingPriorityError
	return isCapError(err) || errors.As(err, &priority)
}

type blockingMember struct {
	key      contract.PopulationProvenance
	resource string
	session  string
}

func (c *capacityCoordinator) pendingExistingAhead(current Definition, resource string) (blockingMember, bool) {
	currentState, _ := c.state.Population(populationKey(current))
	if _, member := findMember(currentState, resource); member == nil || member.SessionName != "" {
		return blockingMember{}, false
	}
	for _, key := range c.definitionKeys() {
		population, err := c.state.Population(key)
		if err != nil || population == nil {
			continue
		}
		for _, member := range population.Members {
			if member == nil || !member.PendingUp || member.SessionName == "" || member.Tombstoned {
				continue
			}
			reason := c.cache.Get(member.SessionName, member.Resource.Locator).LastReason
			if reason != "" && reason != "capacity" {
				continue
			}
			return blockingMember{key: key, resource: member.Resource.Locator, session: member.SessionName}, true
		}
	}
	return blockingMember{}, false
}

type idleCandidate struct {
	session  string
	resource string
	key      contract.PopulationProvenance
	last     time.Time
}

func (c *capacityCoordinator) idleCandidates() ([]idleCandidate, error) {
	sessions, err := c.state.AllE()
	if err != nil {
		return nil, fmt.Errorf("read session state: %w", err)
	}
	var candidates []idleCandidate
	for sessionName, session := range sessions {
		if session == nil || session.Population == nil || !logicalVirtualRootChild(session) || !c.cfg().RunScopeUp(session) {
			continue
		}
		key := *session.Population
		definition, ok := c.definition(key)
		if !ok || !definition.Population.AutoDown {
			continue
		}
		population, err := c.state.Population(key)
		if err != nil {
			// One session's population lookup failing does not invalidate
			// the whole candidate sweep; it is simply not idle-eligible
			// this pass.
			continue
		}
		if population == nil {
			continue
		}
		if session.Resource == nil {
			continue
		}
		member := population.Members[session.Resource.Name]
		if member == nil || member.SessionName != sessionName || member.Tombstoned {
			continue
		}
		status, ok := c.latestStatus(sessionName)
		if !ok || status.Metadata["cleared"] != "true" {
			continue
		}
		activation := session.CreatedAt
		for _, at := range []time.Time{member.LastAppearance, member.LastInbound} {
			if at.After(activation) {
				activation = at
			}
		}
		inbound, inboundErr := c.latestInbound(sessionName)
		if inboundErr != nil {
			continue
		}
		if inbound.After(activation) {
			activation = inbound
		}
		if !status.Time.After(activation) && !status.Time.Equal(activation) {
			continue
		}
		last := activation
		if status.Time.After(last) {
			last = status.Time
		}
		candidates = append(candidates, idleCandidate{session: sessionName, resource: session.Resource.Locator, key: key, last: last})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].last.Equal(candidates[j].last) {
			return candidates[i].last.Before(candidates[j].last)
		}
		return candidates[i].session < candidates[j].session
	})
	return candidates, nil
}

func (c *capacityCoordinator) latestInbound(session string) (time.Time, error) {
	events, err := c.log.Tail(session, event.Filter{Direction: event.Inbound}, 1)
	if err != nil || len(events) == 0 {
		return time.Time{}, err
	}
	return events[0].Time, nil
}

func logicalVirtualRootChild(session *domain.Session) bool {
	return session.ParentSession == "" || strings.HasPrefix(session.ParentSession, "root:")
}

func (c *capacityCoordinator) latestStatus(session string) (event.Event, bool) {
	events, err := c.log.Tail(session, event.Filter{Types: []string{event.TypeStatusMessage}}, 1)
	if err != nil || len(events) == 0 {
		return event.Event{}, false
	}
	return events[0], true
}

func (c *capacityCoordinator) record(candidate idleCandidate, typ, reason, summary string) {
	definition, _ := c.definition(candidate.key)
	_, _, _, _ = c.log.Append(event.Event{
		SessionName: candidate.session,
		Type:        typ,
		Source:      event.SourcePlect,
		Direction:   event.Internal,
		Summary:     summary,
		Metadata: map[string]string{
			"workflow":   definition.Workflow.Address,
			"population": definition.Population.Name,
			"resource":   candidate.resource,
			"reason":     reason,
		},
	})
}
