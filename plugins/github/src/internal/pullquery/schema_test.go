package pullquery

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"
)

type pullResourceDoc struct {
	PullRequest struct {
		StateSchema struct {
			Properties map[string]any `toml:"properties"`
		} `toml:"state_schema"`
	} `toml:"pull_request"`
}

func pullTOMLPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/pullquery -> src -> github -> config/resources/pull_request.toml
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "resources", "pull_request.toml")
}

// This decodes the real, shipped resources/pull_request.toml rather than a
// local copy of its properties, so a future state_schema addition that
// collides with the query's item shape fails here instead of silently
// drifting.
func TestItemSchema_NeverOverlapsStateSchema(t *testing.T) {
	var doc pullResourceDoc
	if _, err := toml.DecodeFile(pullTOMLPath(t), &doc); err != nil {
		t.Fatalf("decode resources/pull_request.toml: %v", err)
	}
	if len(doc.PullRequest.StateSchema.Properties) == 0 {
		t.Fatal("resources/pull_request.toml's [pull_request.state_schema.properties] decoded empty; is the path or table name still current?")
	}
	for _, prop := range ItemSchemaProperties {
		if _, clash := doc.PullRequest.StateSchema.Properties[prop]; clash {
			t.Errorf("item_schema property %q also appears in pull_request.toml's state_schema; the query/observe boundary requires these to be disjoint", prop)
		}
	}
}
