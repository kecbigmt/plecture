package service

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type nodeSubscriptionReleaseObserver struct {
	task.Observer
	cfg         *config.Config
	store       *state.Store
	sessionName string
}

func (o nodeSubscriptionReleaseObserver) OnResult(scope, node, effectID, action, result string, elapsed time.Duration, body string) {
	if inner, ok := o.Observer.(task.ResultObserver); ok {
		inner.OnResult(scope, node, effectID, action, result, elapsed, body)
	}
}

func (o nodeSubscriptionReleaseObserver) OnRelease(released map[string]*contract.TaskState) error {
	if inner, ok := o.Observer.(task.ReleaseObserver); ok {
		if err := inner.OnRelease(released); err != nil {
			return err
		}
	}
	return releaseNodeSubscriptions(o.cfg, o.store, o.sessionName, released)
}

func releaseNodeSubscriptions(cfg *config.Config, store *state.Store, sessionName string, released map[string]*contract.TaskState) error {
	if len(released) == 0 {
		return nil
	}
	if err := store.Update(sessionName, func(s *domain.Session) error {
		for id, st := range released {
			if s.Nodes[id] == nil {
				return fmt.Errorf("node %q is missing while releasing its subscription", id)
			}
			s.Nodes[id] = st
		}
		return nil
	}); err != nil {
		return err
	}
	subscribed := make(map[string]*contract.Resource)
	for id, st := range released {
		if st.Status == contract.TaskStatusCleaned && st.Subscription != nil {
			subscribed[id] = st.Subscription
		}
	}
	for _, resource := range distinctResources(subscribed) {
		if _, msg := unwireDeliveryOnTeardown(cfg, store, sessionName, resource, ""); msg != "" {
			slog.Warn("node subscription teardown failed", "session", sessionName, "resource", resource.Locator, "error", msg)
		}
	}
	return nil
}

func wireProducedNodeSubscriptions(cfg *config.Config, store *state.Store, sessionName string, nodes map[string]*contract.Resource) {
	for _, resource := range distinctResources(nodes) {
		if _, msg := wireDeliveryOnSetup(cfg, store, sessionName, resource, true); msg != "" {
			slog.Warn("node subscription setup failed", "session", sessionName, "resource", resource.Locator, "error", msg)
		} else if err := dequeuePendingSubscribe(store, sessionName, *resource); err != nil {
			slog.Warn("node subscription retry cleanup failed", "session", sessionName, "resource", resource.Locator, "error", err)
		}
	}
}

// Nodes subscribing to one resource in different spellings share one
// registration, so each resource is wired or unwired once.
func distinctResources(byNode map[string]*contract.Resource) []*contract.Resource {
	var out []*contract.Resource
	for _, resource := range byNode {
		if resource == nil {
			continue
		}
		duplicate := false
		for _, seen := range out {
			duplicate = duplicate || sameResource(seen, resource)
		}
		if !duplicate {
			out = append(out, resource)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out
}
