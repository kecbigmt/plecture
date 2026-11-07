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
	resources := make(map[string]bool)
	for _, st := range released {
		if st.Status == contract.TaskStatusCleaned && st.Subscription != "" {
			resources[st.Subscription] = true
		}
	}
	for _, resource := range sortedResources(resources) {
		if _, msg := unwireDeliveryOnTeardown(cfg, store, sessionName, resource, nil, ""); msg != "" {
			slog.Warn("node subscription teardown failed", "session", sessionName, "resource", resource, "error", msg)
		}
	}
	return nil
}

func wireProducedNodeSubscriptions(cfg *config.Config, store *state.Store, sessionName string, nodes map[string]string) {
	resources := make(map[string]bool)
	for _, resource := range nodes {
		resources[resource] = true
	}
	for _, resource := range sortedResources(resources) {
		if _, msg := wireDeliveryOnSetup(cfg, store, sessionName, resource, nil, true); msg != "" {
			slog.Warn("node subscription setup failed", "session", sessionName, "resource", resource, "error", msg)
		} else if err := dequeuePendingSubscribe(store, sessionName, resource); err != nil {
			slog.Warn("node subscription retry cleanup failed", "session", sessionName, "resource", resource, "error", err)
		}
	}
}

func sortedResources(resources map[string]bool) []string {
	out := make([]string, 0, len(resources))
	for resource := range resources {
		out = append(out, resource)
	}
	sort.Strings(out)
	return out
}
