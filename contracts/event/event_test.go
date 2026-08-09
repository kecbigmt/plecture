package event

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMatchType(t *testing.T) {
	cases := []struct {
		pattern, typ string
		want         bool
	}{
		{"*", "github.ci_status", true},
		{"github.ci_status", "github.ci_status", true},
		{"github.ci_status", "github.state", false},
		{"github.*", "github.ci_status", true},
		{"github.*", "github.", true},
		{"github.*", "github", false}, // prefix is "github." — bare "github" excluded
		{"github.*", "slack.message", false},
		{"claude.*", "claude.reply", true},
	}
	for _, c := range cases {
		if got := MatchType(c.pattern, c.typ); got != c.want {
			t.Errorf("MatchType(%q, %q) = %v, want %v", c.pattern, c.typ, got, c.want)
		}
	}
}

func TestFilterMatch(t *testing.T) {
	ev := Event{Type: "github.ci_status", Source: SourceGitHub, Direction: Internal}

	cases := []struct {
		name string
		f    Filter
		want bool
	}{
		{"zero filter matches all", Filter{}, true},
		{"type glob hit", Filter{Types: []string{"github.*"}}, true},
		{"type glob miss", Filter{Types: []string{"claude.*"}}, false},
		{"type multi any-hit", Filter{Types: []string{"claude.*", "github.*"}}, true},
		{"source hit", Filter{Sources: []string{SourceGitHub}}, true},
		{"source miss", Filter{Sources: []string{SourceSlack}}, false},
		{"direction hit", Filter{Direction: Internal}, true},
		{"direction miss", Filter{Direction: Inbound}, false},
		{"combined hit", Filter{Types: []string{"github.*"}, Sources: []string{SourceGitHub}, Direction: Internal}, true},
		{"combined one miss", Filter{Types: []string{"github.*"}, Direction: Outbound}, false},
	}
	for _, c := range cases {
		if got := c.f.Match(ev); got != c.want {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFilterMatchStreamID(t *testing.T) {
	tagged := Event{Type: "claude.reply", StreamID: "stream-abc"}
	untagged := Event{Type: "claude.reply"}

	cases := []struct {
		name string
		f    Filter
		ev   Event
		want bool
	}{
		{"empty stream filter matches tagged", Filter{}, tagged, true},
		{"empty stream filter matches untagged", Filter{}, untagged, true},
		{"stream hit", Filter{StreamID: "stream-abc"}, tagged, true},
		{"stream miss (different)", Filter{StreamID: "stream-xyz"}, tagged, false},
		{"stream miss (untagged event)", Filter{StreamID: "stream-abc"}, untagged, false},
	}
	for _, c := range cases {
		if got := c.f.Match(c.ev); got != c.want {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFilterMatchDeliveryMode(t *testing.T) {
	pushed := Event{Type: TypeTerminalDone, DeliveryMode: DeliveryModePush}
	pulled := Event{Type: "github.ci_status"}

	cases := []struct {
		name string
		f    Filter
		ev   Event
		want bool
	}{
		{"empty delivery filter matches push", Filter{}, pushed, true},
		{"empty delivery filter matches pull (zero value)", Filter{}, pulled, true},
		{"delivery hit", Filter{DeliveryMode: DeliveryModePush}, pushed, true},
		{"delivery miss (pull requested, push event)", Filter{DeliveryMode: DeliveryModePull}, pushed, false},
		{"delivery miss (push requested, zero-value pull event)", Filter{DeliveryMode: DeliveryModePush}, pulled, false},
		{"pull filter matches an event with empty (unset) delivery_mode", Filter{DeliveryMode: DeliveryModePull}, pulled, true},
	}
	for _, c := range cases {
		if got := c.f.Match(c.ev); got != c.want {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDeliveryModeNormalize(t *testing.T) {
	cases := []struct {
		name string
		m    DeliveryMode
		want DeliveryMode
	}{
		{"empty normalizes to pull", "", DeliveryModePull},
		{"pull stays pull", DeliveryModePull, DeliveryModePull},
		{"push stays push", DeliveryModePush, DeliveryModePush},
	}
	for _, c := range cases {
		if got := c.m.Normalize(); got != c.want {
			t.Errorf("%s: Normalize() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"blank", "   ", nil},
		{"single", "github", []string{"github"}},
		{"comma no space", "github,slack", []string{"github", "slack"}},
		{"comma with space", "github, slack", []string{"github", "slack"}},
		{"leading/trailing space", "  github , slack  ", []string{"github", "slack"}},
		{"empty element dropped", "github,,slack", []string{"github", "slack"}},
		{"all-blank elements", " , , ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitCSV(c.in)
			if !slices.Equal(got, c.want) {
				t.Errorf("SplitCSV(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestStreamIDJSONRoundTrip(t *testing.T) {
	// omitempty: an unset StreamID must not emit the key.
	out, err := json.Marshal(Event{ID: "01", Type: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "stream_id") {
		t.Errorf("empty StreamID should omit the key, got %s", out)
	}

	// A set StreamID survives a marshal/unmarshal round trip.
	in := Event{ID: "02", Type: "t", StreamID: "stream-abc"}
	out, err = json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"stream_id":"stream-abc"`) {
		t.Errorf("set StreamID missing from JSON: %s", out)
	}
	var back Event
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.StreamID != "stream-abc" {
		t.Errorf("round trip lost StreamID: got %q", back.StreamID)
	}
}
