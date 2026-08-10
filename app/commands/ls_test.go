package commands

import (
	"testing"
	"time"

	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/service"
)

func TestFormatMessage_UnsetRendersDash(t *testing.T) {
	if got := formatMessage(service.ListEntry{}); got != "-" {
		t.Errorf("formatMessage() = %q, want %q", got, "-")
	}
}

func TestFormatMessage_EmptyTextRendersDash(t *testing.T) {
	e := service.ListEntry{Message: &domain.Message{Text: "", UpdatedAt: time.Now()}}
	if got := formatMessage(e); got != "-" {
		t.Errorf("formatMessage() = %q, want %q", got, "-")
	}
}

func TestFormatMessage_ShortTextRendersInFull(t *testing.T) {
	e := service.ListEntry{Message: &domain.Message{Text: "working", UpdatedAt: time.Now().Add(-5 * time.Second)}}
	got := formatMessage(e)
	want := "working (5s)"
	if got != want {
		t.Errorf("formatMessage() = %q, want %q", got, want)
	}
}

// A message longer than the display limit is truncated with an ellipsis so
// the ls table stays scannable.
func TestFormatMessage_LongTextIsTruncated(t *testing.T) {
	e := service.ListEntry{Message: &domain.Message{
		Text:      "this is a much longer status message than fits in the column",
		UpdatedAt: time.Now(),
	}}
	got := formatMessage(e)
	want := "this is a much longer st… (0s)"
	if got != want {
		t.Errorf("formatMessage() = %q, want %q", got, want)
	}
}

func TestFormatLastActive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"future timestamp reads as just now", now.Add(time.Minute), "just now"},
		{"seconds", now.Add(-30 * time.Second), "30s"},
		{"minutes", now.Add(-5 * time.Minute), "5m"},
		{"hours", now.Add(-3 * time.Hour), "3h"},
		{"days", now.Add(-48 * time.Hour), "2d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLastActive(tt.t); got != tt.want {
				t.Errorf("formatLastActive() = %q, want %q", got, tt.want)
			}
		})
	}
}

// taskDisplayName shows only the instance key when it matches the task
// name (the common case), and "name (instance)" when they diverge — e.g. a
// numbered instance of a repeatable task.
func TestTaskDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		instance string
		want     string
	}{
		{"name equals instance", "review", "review", "review"},
		{"empty name", "", "review#1", "review#1"},
		{"name diverges from instance", "review", "review#1", "review (review#1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskDisplayName(tt.taskName, tt.instance); got != tt.want {
				t.Errorf("taskDisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}
