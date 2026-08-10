package webui

import (
	"testing"

	"github.com/kecbigmt/sennit/app/internal/domain"
)

// statusClass must return a distinct, non-empty class per known Run/Health
// value so the badges are visually distinguishable.
func TestStatusClass_DistinctAndNonEmpty(t *testing.T) {
	values := []any{
		domain.RunUp,
		domain.RunDown,
		domain.HealthHealthy,
		domain.HealthUnhealthy,
		domain.HealthUndeclared,
		domain.RunState("untracked"),
	}
	for _, v := range values {
		if statusClass(v) == "" {
			t.Errorf("statusClass(%v) is empty", v)
		}
	}
	if statusClass(domain.HealthHealthy) == statusClass(domain.HealthUnhealthy) ||
		statusClass(domain.HealthUnhealthy) == statusClass(domain.HealthUndeclared) ||
		statusClass(domain.HealthHealthy) == statusClass(domain.HealthUndeclared) {
		t.Error("healthy/unhealthy/undeclared badge classes must be mutually distinct")
	}
	if statusClass(domain.RunUp) == statusClass(domain.RunDown) {
		t.Error("up/down badge classes must be distinct")
	}
}
