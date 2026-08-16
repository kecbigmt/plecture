package dispatch

import (
	"log/slog"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// recordChannelFailure appends one failure to the session's channel
// validation/delivery streak (contract.ChannelHealth). Validation and
// delivery failures share one streak because they mean the same thing to a
// parent deciding whether to react: this session's event channel isn't
// working. The escalation decision itself is not made here — it lives in
// internal/service (service.CheckChannelHealth, driven by the reactor)
// because it needs service.Up's wake-if-down behavior, and internal/service
// already imports internal/dispatch (see internal/reactor/supervisor.go's
// package doc for the identical constraint on tick), so this package cannot
// call back into it.
func recordChannelFailure(st *state.Store, session, kind, channelName string, cause error, at time.Time) {
	err := st.Update(session, func(s *domain.Session) error {
		ch := s.ChannelHealth
		if ch == nil {
			ch = &contract.ChannelHealth{}
		}
		if ch.ConsecutiveFailures == 0 {
			ch.FirstFailureAt = at
		}
		ch.ConsecutiveFailures++
		ch.LastFailureAt = at
		ch.LastKind = kind
		ch.LastChannel = channelName
		if cause != nil {
			ch.LastError = cause.Error()
		}
		s.ChannelHealth = ch
		s.UpdatedAt = at
		return nil
	})
	if err != nil {
		slog.Default().Warn("dispatcher: persist channel failure failed", "session", session, "error", err)
	}
}

// recordChannelSuccess clears an open failure streak, ending the episode; a
// later failure starts a new one and is free to escalate again. The cheap
// Get check avoids a state.json rewrite on the path where nothing needs
// clearing — the overwhelming majority of deliveries and validations.
func recordChannelSuccess(st *state.Store, session string) {
	if s := st.Get(session); s == nil || s.ChannelHealth == nil || s.ChannelHealth.ConsecutiveFailures == 0 {
		return
	}
	err := st.Update(session, func(s *domain.Session) error {
		if s.ChannelHealth == nil || s.ChannelHealth.ConsecutiveFailures == 0 {
			return nil
		}
		s.ChannelHealth = &contract.ChannelHealth{}
		s.UpdatedAt = time.Now()
		return nil
	})
	if err != nil {
		slog.Default().Warn("dispatcher: clear channel failure failed", "session", session, "error", err)
	}
}
