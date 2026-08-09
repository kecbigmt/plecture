package commands

import (
	"slices"
	"testing"
)

func TestSplitTypesArg(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"single repeatable", []string{"github.*"}, []string{"github.*"}},
		{"repeatable, no comma", []string{"github.*", "claude.reply"}, []string{"github.*", "claude.reply"}},
		{"single comma-separated", []string{"github.*,claude.reply"}, []string{"github.*", "claude.reply"}},
		{"comma-separated with whitespace", []string{"github.*, claude.reply "}, []string{"github.*", "claude.reply"}},
		{"repeatable each comma-separated", []string{"github.*,claude.reply", "slack.message"}, []string{"github.*", "claude.reply", "slack.message"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitTypesArg(c.in)
			if !slices.Equal(got, c.want) {
				t.Errorf("splitTypesArg(%#v) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

// buildEventFilter reads package-level flag vars set by cobra; this exercises
// it the same way the CLI/MCP handlers must agree: comma-separated and
// repeatable --type inputs both split and trim, matching MCP's types/source.
func TestBuildEventFilter_SplitsAndTrimsTypesAndSources(t *testing.T) {
	origTypes, origSource := eventTypes, eventSource
	origDirection, origDelivery, origLimit := eventDirection, eventDelivery, eventLimit
	t.Cleanup(func() {
		eventTypes, eventSource = origTypes, origSource
		eventDirection, eventDelivery, eventLimit = origDirection, origDelivery, origLimit
	})

	eventTypes = []string{"github.*, claude.reply", "slack.message"}
	eventSource = " github, slack "
	eventDirection = "inbound"
	eventDelivery = "push"
	eventLimit = 5

	f := buildEventFilter()

	wantTypes := []string{"github.*", "claude.reply", "slack.message"}
	if !slices.Equal(f.Types, wantTypes) {
		t.Errorf("Types = %#v, want %#v", f.Types, wantTypes)
	}
	wantSources := []string{"github", "slack"}
	if !slices.Equal(f.Sources, wantSources) {
		t.Errorf("Sources = %#v, want %#v", f.Sources, wantSources)
	}
}
