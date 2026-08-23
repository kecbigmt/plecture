package state

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

// TestLayerState_JSONUsesEffectID pins the wire shape docs/migrations
// /task-layer-effect-id-migration.md ships a one-time migration for: the
// persisted key is effect_id, not the pre-rename task_id.
func TestLayerState_JSONUsesEffectID(t *testing.T) {
	raw, err := json.Marshal(LayerState{EffectID: "outer", Status: TaskStatusProduced})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"effect_id":"outer"`)) {
		t.Errorf("marshaled LayerState = %s, want an effect_id key", raw)
	}
	if bytes.Contains(raw, []byte(`"task_id"`)) {
		t.Errorf("marshaled LayerState = %s, want no task_id key", raw)
	}

	var decoded LayerState
	if err := json.Unmarshal([]byte(`{"effect_id":"inner","status":"produced"}`), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.EffectID != "inner" {
		t.Errorf("EffectID = %q, want %q", decoded.EffectID, "inner")
	}
}

// migrationFilter is docs/migrations/task-layer-effect-id-migration.md's
// "State changes" jq filter, verbatim. Keep the two in sync.
const migrationFilter = `(.sessions[]? | .tasks[]? | select(.layers != null) | .layers[]) |=
      (if has("task_id") then (. + {effect_id: .task_id} | del(.task_id)) else . end)`

// runMigrationFilter shells out to jq the same way the documented migration
// procedure does, skipping the test where jq is unavailable rather than
// failing a build that has no other reason to depend on it.
func runMigrationFilter(t *testing.T, input string) string {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	cmd := exec.Command("jq", "-c", migrationFilter)
	cmd.Stdin = bytes.NewBufferString(input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("jq: %v: %s", err, stderr.String())
	}
	return out.String()
}

// layerIDs extracts every layer's identity (effect_id if present, else
// task_id) from a state.json document, for an order-independent identity
// comparison between two migration passes.
func layerIDs(t *testing.T, doc string) map[string]bool {
	t.Helper()
	var parsed struct {
		Sessions map[string]struct {
			Tasks map[string]struct {
				Layers []map[string]any `json:"layers"`
			} `json:"tasks"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range parsed.Sessions {
		for _, task := range s.Tasks {
			for _, layer := range task.Layers {
				id, _ := layer["effect_id"].(string)
				if id == "" {
					id, _ = layer["task_id"].(string)
				}
				ids[id] = true
			}
		}
	}
	return ids
}

// TestMigrationFilter_FreshStateRenamesTaskIDToEffectID covers the
// straightforward case: every layer still carries the pre-rename key.
func TestMigrationFilter_FreshStateRenamesTaskIDToEffectID(t *testing.T) {
	const fresh = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"task_id":"outer","status":"produced"},
		{"task_id":"inner","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, fresh)
	if bytes.Contains([]byte(got), []byte(`"task_id"`)) {
		t.Errorf("migrated = %s, want no task_id keys left", got)
	}
	before := layerIDs(t, fresh)
	after := layerIDs(t, got)
	if len(after) != len(before) {
		t.Errorf("layer identities = %v, want %v preserved", after, before)
	}
	for id := range before {
		if !after[id] {
			t.Errorf("layer %q lost after migration (got %v)", id, after)
		}
	}
}

// TestMigrationFilter_AlreadyMigratedStateIsUnchanged is the regression test
// for the bug a reviewer found in this migration's first draft: a layer that
// already carries effect_id (task_id already deleted) has no "task_id" key,
// so `. + {effect_id: .task_id}` read a missing key as JSON null and
// overwrote the good effect_id with it. Rerunning the filter — the exact
// scenario a partially-applied or re-executed migration hits — must leave an
// already-migrated layer's identity untouched.
func TestMigrationFilter_AlreadyMigratedStateIsUnchanged(t *testing.T) {
	const migrated = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"effect_id":"outer","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, migrated)
	ids := layerIDs(t, got)
	if !ids["outer"] {
		t.Fatalf("migrated = %s, want effect_id %q preserved, not nulled", got, "outer")
	}
	if len(ids) != 1 {
		t.Errorf("layer identities = %v, want exactly {outer}", ids)
	}
}

// TestMigrationFilter_PartiallyMigratedStateMigratesOnlyWhatNeedsIt covers a
// state.json where one layer already migrated and its sibling has not — the
// shape a migration interrupted mid-run, or applied session-by-session,
// leaves behind.
func TestMigrationFilter_PartiallyMigratedStateMigratesOnlyWhatNeedsIt(t *testing.T) {
	const mixed = `{"sessions":{"s":{"tasks":{"outer":{"layers":[
		{"effect_id":"outer","status":"produced"},
		{"task_id":"inner","status":"produced"}
	]}}}}}`
	got := runMigrationFilter(t, mixed)
	if bytes.Contains([]byte(got), []byte(`"task_id"`)) {
		t.Errorf("migrated = %s, want no task_id keys left", got)
	}
	ids := layerIDs(t, got)
	for _, want := range []string{"outer", "inner"} {
		if !ids[want] {
			t.Errorf("layer identities = %v, want %q preserved", ids, want)
		}
	}
}
