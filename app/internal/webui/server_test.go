package webui

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/domain"
)

// statusClass must return a distinct, non-empty class per known Run/Health
// value so the badges are visually distinguishable.
func TestStatusClass_DistinctAndNonEmpty(t *testing.T) {
	values := []any{
		domain.RunUp,
		domain.RunDown,
		domain.HealthHealthy,
		domain.HealthUnhealthy,
		domain.HealthStalled,
		domain.HealthUndeclared,
		domain.RunState("untracked"),
	}
	for _, v := range values {
		if statusClass(v) == "" {
			t.Errorf("statusClass(%v) is empty", v)
		}
	}
	healthValues := []domain.HealthState{
		domain.HealthHealthy,
		domain.HealthUnhealthy,
		domain.HealthStalled,
		domain.HealthUndeclared,
	}
	for i, a := range healthValues {
		for _, b := range healthValues[i+1:] {
			if statusClass(a) == statusClass(b) {
				t.Errorf("healthy/unhealthy/stalled/undeclared badge classes must be mutually distinct: %v and %v both got %q", a, b, statusClass(a))
			}
		}
	}
	if statusClass(domain.RunUp) == statusClass(domain.RunDown) {
		t.Error("up/down badge classes must be distinct")
	}
	if statusClass(domain.HealthStalled) == statusClass(domain.HealthState("unknown-fallback")) {
		t.Error("stalled must have its own badge class, not fall back to the unknown-value default")
	}
}
