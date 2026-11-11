package service

import (
	"fmt"
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
	projectRoot string
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
	return releaseNodeSubscriptions(o.cfg, o.store, o.sessionName, o.projectRoot, released)
}

func releaseNodeSubscriptions(cfg *config.Config, store *state.Store, sessionName, projectRoot string, released map[string]*contract.TaskState) error {
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
	return nil
}
