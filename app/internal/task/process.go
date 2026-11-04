package task

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/procid"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// CaptureProcess records the identity of the process an effect's `process`
// alive probe names, at the moment setup produced it, so a later probe can
// tell that process apart from an unrelated one that reuses its pid. It
// returns nil for an effect declaring no such probe. A pid that is absent,
// malformed, or not running fails setup: the effect declared that its output
// names a live process and it does not.
func CaptureProcess(health *config.HealthConfig, outputs map[string]any) (*contract.ProcessIdentity, error) {
	action := health.AliveProbe()
	if action == nil || action.Process == nil {
		return nil, nil
	}
	pid, err := processPID(action, outputs)
	if err != nil {
		return nil, fmt.Errorf("process probe: %w", err)
	}
	identity, err := procid.Capture(pid)
	if err != nil {
		return nil, fmt.Errorf("process probe: %w", err)
	}
	return &identity, nil
}

// captureNestedProcess is CaptureProcess for a nesting chain: the declaring
// layer's `self.outputs` is that layer's own contract, the same view its probe
// reads later, so the pid is taken from there rather than from the composed
// task's outputs, which may name the key differently or omit it.
func captureNestedProcess(r Resolved, states []contract.LayerState, session SessionVars) (*contract.ProcessIdentity, error) {
	i := effect.ProcessLayer(r.Layers)
	if i < 0 {
		return nil, nil
	}
	views, err := ProjectLayerOutputs(r.Layers, states, session)
	if err != nil {
		return nil, err
	}
	return CaptureProcess(r.Layers[i].Health, views[i])
}

// verifyProcess is the core-evaluated half of an alive probe. A record with
// no stored identity predates the probe and is judged by pid existence alone;
// a record whose pid is no longer the one the output names is stale, which is
// a disagreement between two authorities rather than a verdict either could
// give alone.
func verifyProcess(p Probe) error {
	pid, err := processPID(p.Action, p.Self)
	if err != nil {
		return err
	}
	if p.Process == nil {
		return procid.VerifyPIDOnly(pid)
	}
	if p.Process.PID != pid {
		return fmt.Errorf("output names pid %d but pid %d was recorded", pid, p.Process.PID)
	}
	return procid.Verify(*p.Process)
}

func processPID(action *lang.Action, outputs map[string]any) (int, error) {
	eval := lang.Eval{Roots: lang.Roots{"self": map[string]any{"outputs": orEmpty(normalizeOutputs(outputs))}}}
	raw, _, err := eval.Value(action.Process)
	if err != nil {
		return 0, err
	}
	pid, ok := asPID(raw)
	if !ok {
		return 0, fmt.Errorf("%s is %v, not a process id", action.Process.From, raw)
	}
	return pid, nil
}

func asPID(v any) (int, bool) {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case float64:
		if x != math.Trunc(x) {
			return 0, false
		}
		n = int64(x)
	case json.Number:
		parsed, err := strconv.ParseInt(x.String(), 10, 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	case string:
		// A pid some probe hooks and scenario fixtures state as text.
		parsed, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if n <= 0 || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}
